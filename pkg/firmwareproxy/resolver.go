package firmwareproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/mod/semver"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

const FirmwareBundleArtifactType = "application/vnd.openchami.firmware.bundle.v1+json"

const (
	annotationCompatibleHardware = "dev.fabrica.hardware.compatible"
	annotationImageVersion       = "org.opencontainers.image.version"
)

type HTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return fmt.Sprintf("http status %d", e.StatusCode)
	}
	return fmt.Sprintf("http status %d: %s", e.StatusCode, e.Message)
}

type payloadLocation struct {
	Repository string
}

type DiscoveryResult struct {
	Version      string
	Digest       string
	OCIReference string
}

type manifestCandidate struct {
	tag               string
	versionRaw        string
	versionNormalized string
	payloadDigest     string
}

type authConfig struct {
	username string
	password string
}

var payloadIndex sync.Map
var authState sync.RWMutex
var globalAuthConfig authConfig

// InitAuth configures global OCI registry credentials used by ORAS remote repositories.
func InitAuth(username, password string) {
	authState.Lock()
	globalAuthConfig = authConfig{
		username: strings.TrimSpace(username),
		password: strings.TrimSpace(password),
	}
	authState.Unlock()
}

func ResolvePayload(ctx context.Context, ociReference string) (string, error) {
	parsed, err := registry.ParseReference(ociReference)
	if err != nil {
		return "", fmt.Errorf("parse OCI reference: %w", err)
	}

	repo, err := remote.NewRepository(parsed.Registry + "/" + parsed.Repository)
	if err != nil {
		return "", fmt.Errorf("create ORAS repository client: %w", err)
	}
	repo.PlainHTTP = isLoopbackRegistry(parsed.Registry)
	applyRepoAuth(repo)

	reference := parsed.ReferenceOrDefault()
	_, manifestBytes, err := oras.FetchBytes(ctx, repo, reference, oras.FetchBytesOptions{})
	if err != nil {
		return "", classifyORASError(fmt.Errorf("fetch manifest for %q: %w", reference, err))
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", fmt.Errorf("decode OCI manifest: %w", err)
	}
	if manifest.ArtifactType != FirmwareBundleArtifactType {
		return "", &HTTPStatusError{
			StatusCode: 400,
			Message:    fmt.Sprintf("unexpected artifactType %q (expected %q)", manifest.ArtifactType, FirmwareBundleArtifactType),
		}
	}
	if len(manifest.Layers) == 0 {
		return "", &HTTPStatusError{StatusCode: 400, Message: "firmware bundle has no layers"}
	}

	payloadDigest := manifest.Layers[0].Digest.String()
	payloadIndex.Store(payloadDigest, payloadLocation{Repository: parsed.Registry + "/" + parsed.Repository})

	return payloadDigest, nil
}

func ResolvePayloadFromDiscovery(ctx context.Context, repository, hardwareModel, versionTarget string) (DiscoveryResult, error) {
	repo, err := remote.NewRepository(strings.TrimSpace(repository))
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("create ORAS repository client: %w", err)
	}
	repo.PlainHTTP = isLoopbackRegistry(repo.Reference.Registry)
	applyRepoAuth(repo)

	var tags []string
	if err := repo.Tags(ctx, "", func(batch []string) error {
		tags = append(tags, batch...)
		return nil
	}); err != nil {
		return DiscoveryResult{}, classifyORASError(fmt.Errorf("list tags for %q: %w", repository, err))
	}

	if len(tags) == 0 {
		return DiscoveryResult{}, &HTTPStatusError{StatusCode: 404, Message: fmt.Sprintf("no tags found in repository %q", repository)}
	}

	candidates := make([]manifestCandidate, 0, len(tags))
	for _, tag := range tags {
		_, manifestBytes, err := oras.FetchBytes(ctx, repo, tag, oras.FetchBytesOptions{})
		if err != nil {
			continue
		}

		var manifest ocispec.Manifest
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			continue
		}

		candidate, ok := buildManifestCandidate(manifest, tag, hardwareModel)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate)
	}

	selected, err := selectManifestCandidate(candidates, versionTarget)
	if err != nil {
		return DiscoveryResult{}, err
	}

	payloadIndex.Store(selected.payloadDigest, payloadLocation{Repository: repository})

	return DiscoveryResult{
		Version:      selected.versionRaw,
		Digest:       selected.payloadDigest,
		OCIReference: fmt.Sprintf("%s:%s", repository, selected.tag),
	}, nil
}

func buildManifestCandidate(manifest ocispec.Manifest, tag, hardwareModel string) (manifestCandidate, bool) {
	if manifest.ArtifactType != FirmwareBundleArtifactType {
		return manifestCandidate{}, false
	}

	if len(manifest.Layers) == 0 {
		return manifestCandidate{}, false
	}

	compatible := strings.TrimSpace(manifest.Annotations[annotationCompatibleHardware])
	if !isCompatibleHardware(compatible, hardwareModel) {
		return manifestCandidate{}, false
	}

	versionRaw := strings.TrimSpace(manifest.Annotations[annotationImageVersion])
	versionNormalized, ok := normalizeSemver(versionRaw)
	if !ok {
		return manifestCandidate{}, false
	}

	return manifestCandidate{
		tag:               tag,
		versionRaw:        versionRaw,
		versionNormalized: versionNormalized,
		payloadDigest:     manifest.Layers[0].Digest.String(),
	}, true
}

func selectManifestCandidate(candidates []manifestCandidate, versionTarget string) (manifestCandidate, error) {
	if len(candidates) == 0 {
		return manifestCandidate{}, &HTTPStatusError{StatusCode: 404, Message: "no compatible firmware manifests found"}
	}

	sort.Slice(candidates, func(i, j int) bool {
		cmp := semver.Compare(candidates[i].versionNormalized, candidates[j].versionNormalized)
		if cmp == 0 {
			return candidates[i].tag < candidates[j].tag
		}
		return cmp > 0
	})

	if strings.EqualFold(strings.TrimSpace(versionTarget), "latest") {
		return candidates[0], nil
	}

	normalizedTarget, ok := normalizeSemver(versionTarget)
	if !ok {
		return manifestCandidate{}, &HTTPStatusError{StatusCode: 400, Message: fmt.Sprintf("invalid discovery version target %q", versionTarget)}
	}

	for _, candidate := range candidates {
		if candidate.versionNormalized == normalizedTarget {
			return candidate, nil
		}
	}

	return manifestCandidate{}, &HTTPStatusError{StatusCode: 404, Message: fmt.Sprintf("no compatible manifest found for version %q", versionTarget)}
}

func normalizeSemver(version string) (string, bool) {
	v := strings.TrimSpace(version)
	if v == "" {
		return "", false
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	return v, true
}

func isCompatibleHardware(compatibilityAnnotation, hardwareModel string) bool {
	requested := strings.ToLower(strings.TrimSpace(hardwareModel))
	if requested == "" {
		return false
	}

	for _, token := range strings.FieldsFunc(compatibilityAnnotation, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	}) {
		if strings.EqualFold(strings.TrimSpace(token), requested) {
			return true
		}
	}

	return false
}

func StreamPayloadLayer(ctx context.Context, digestStr string) (io.ReadCloser, int64, error) {
	if _, parseErr := digest.Parse(digestStr); parseErr != nil {
		return nil, 0, &HTTPStatusError{StatusCode: 400, Message: fmt.Sprintf("invalid digest %q", digestStr)}
	}

	locAny, found := payloadIndex.Load(digestStr)
	if !found {
		return nil, 0, &HTTPStatusError{StatusCode: 404, Message: "unknown payload digest"}
	}
	loc, ok := locAny.(payloadLocation)
	if !ok {
		return nil, 0, fmt.Errorf("invalid payload index entry for digest %q", digestStr)
	}

	repo, err := remote.NewRepository(loc.Repository)
	if err != nil {
		return nil, 0, fmt.Errorf("create ORAS repository client: %w", err)
	}
	repo.PlainHTTP = isLoopbackRegistry(repo.Reference.Registry)
	applyRepoAuth(repo)

	desc, err := repo.Blobs().Resolve(ctx, digestStr)
	if err != nil {
		return nil, 0, classifyORASError(fmt.Errorf("resolve payload layer %q: %w", digestStr, err))
	}

	rc, err := repo.Blobs().Fetch(ctx, desc)
	if err != nil {
		return nil, 0, classifyORASError(fmt.Errorf("stream payload layer %q: %w", digestStr, err))
	}

	return rc, desc.Size, nil
}

func isLoopbackRegistry(registryHost string) bool {
	host := registryHost
	if strings.HasPrefix(host, "[") && strings.Contains(host, "]") {
		trimmed := strings.TrimPrefix(host, "[")
		host = strings.SplitN(trimmed, "]", 2)[0]
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	host = strings.TrimSpace(host)
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func classifyORASError(err error) error {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "status code 400") ||
		strings.Contains(msg, "status code 401") ||
		strings.Contains(msg, "status code 403") ||
		strings.Contains(msg, "status code 404") ||
		strings.Contains(msg, "status code 405") ||
		strings.Contains(msg, "status code 409") {
		return &HTTPStatusError{StatusCode: 400, Message: err.Error()}
	}

	if strings.Contains(msg, "status code 429") ||
		strings.Contains(msg, "status code 500") ||
		strings.Contains(msg, "status code 502") ||
		strings.Contains(msg, "status code 503") ||
		strings.Contains(msg, "status code 504") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "temporary") {
		return &HTTPStatusError{StatusCode: 503, Message: err.Error()}
	}

	return err
}

func applyRepoAuth(repo *remote.Repository) {
	if repo == nil {
		return
	}

	authState.RLock()
	username := globalAuthConfig.username
	password := globalAuthConfig.password
	authState.RUnlock()

	if username == "" || password == "" {
		return
	}

	repo.Client = &auth.Client{
		Client: http.DefaultClient,
		Credential: auth.StaticCredential(repo.Reference.Registry, auth.Credential{
			Username: username,
			Password: password,
		}),
		Cache: auth.NewCache(),
	}
}
