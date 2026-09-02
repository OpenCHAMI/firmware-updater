package firmwareproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/user/firmware-updater/pkg/debug"
	"github.com/user/firmware-updater/pkg/semverutil"
	"golang.org/x/mod/semver"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

const FirmwareBundleArtifactType = "application/vnd.openchami.firmware.bundle.v1+json"

const envRepositoryInsecureTLS = "FIRMWARE_UPDATER_REPOSITORY_INSECURE_TLS"

const (
	annotationCompatibleHardware    = "dev.fabrica.hardware.compatible"
	annotationFirmwareModels        = "dev.fabrica.firmware.models"
	annotationFirmwareSoftwareID    = "dev.fabrica.firmware.softwareIds"
	annotationFirmwareVersionString = "dev.fabrica.firmware.versionString"
	annotationImageVersion          = "org.opencontainers.image.version"
	annotationImageTitle            = "org.opencontainers.image.title"
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
	Version         string
	VersionString   string
	Digest          string
	OCIReference    string
	PayloadFilename string
}

type manifestCandidate struct {
	tag               string
	versionRaw        string
	versionString     string
	versionNormalized string
	payloadDigest     string
	payloadFilename   string
}

type inventoryIdentity struct {
	targets    []string
	model      string
	softwareID string
}

type authConfig struct {
	username string
	password string
}

var payloadIndex sync.Map
var authState sync.RWMutex
var globalAuthConfig authConfig

// insecureRegistryHTTPClient is used only when the explicit insecure OCI
// transport override is enabled to accept an untrusted registry certificate.
var insecureRegistryHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// INSECURE CALL: TLS verification disabled for OCI registry connections.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // INSECURE CALL: fix later
	},
}

// InitAuth configures global OCI registry credentials used by ORAS remote repositories.
func InitAuth(username, password string) {
	authState.Lock()
	globalAuthConfig = authConfig{
		username: strings.TrimSpace(username),
		password: strings.TrimSpace(password),
	}
	authState.Unlock()
}

// NewRepository creates an authenticated OCI repository client.
func NewRepository(reference string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(strings.TrimSpace(reference))
	if err != nil {
		return nil, err
	}
	// OCI registries are always contacted over HTTPS. The insecure override
	// changes certificate verification in applyRepoAuth, not the protocol.
	repo.PlainHTTP = false
	applyRepoAuth(repo)
	return repo, nil
}

// NewRegistry creates an authenticated OCI registry client.
func NewRegistry(host string) (*remote.Registry, error) {
	registryClient, err := remote.NewRegistry(strings.TrimSpace(host))
	if err != nil {
		return nil, err
	}
	registryClient.PlainHTTP = false

	probeRepo, err := NewRepository(strings.TrimSpace(host) + "/firmware-updater-auth-probe")
	if err != nil {
		return nil, err
	}
	registryClient.Client = probeRepo.Client
	return registryClient, nil
}

func ResolvePayload(ctx context.Context, ociReference string) (DiscoveryResult, error) {
	if debug.IsEnabled() {
		defer debug.Trace("firmwareproxy.ResolvePayload", "ociReference", ociReference)()
	}

	parsed, err := registry.ParseReference(ociReference)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("parse OCI reference: %w", err)
	}

	repo, err := NewRepository(parsed.Registry + "/" + parsed.Repository)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("create ORAS repository client: %w", err)
	}

	reference := parsed.ReferenceOrDefault()
	_, manifestBytes, err := oras.FetchBytes(ctx, repo, reference, oras.FetchBytesOptions{})
	if err != nil {
		return DiscoveryResult{}, classifyORASError(fmt.Errorf("fetch manifest for %q: %w", reference, err))
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return DiscoveryResult{}, fmt.Errorf("decode OCI manifest: %w", err)
	}
	if manifest.ArtifactType != FirmwareBundleArtifactType {
		return DiscoveryResult{}, &HTTPStatusError{
			StatusCode: 400,
			Message:    fmt.Sprintf("unexpected artifactType %q (expected %q)", manifest.ArtifactType, FirmwareBundleArtifactType),
		}
	}
	if len(manifest.Layers) == 0 {
		return DiscoveryResult{}, &HTTPStatusError{StatusCode: 400, Message: "firmware bundle has no layers"}
	}

	payloadLayer := manifest.Layers[0]
	payloadDigest := payloadLayer.Digest.String()
	payloadFilename := strings.TrimSpace(payloadLayer.Annotations[annotationImageTitle])
	payloadIndex.Store(payloadDigest, payloadLocation{Repository: parsed.Registry + "/" + parsed.Repository})

	return DiscoveryResult{Digest: payloadDigest, OCIReference: ociReference, PayloadFilename: payloadFilename}, nil
}

func ResolvePayloadFromDiscovery(ctx context.Context, repository, hardwareModel, versionTarget string) (DiscoveryResult, error) {
	if debug.IsEnabled() {
		defer debug.Trace("firmwareproxy.ResolvePayloadFromDiscovery", "repository", repository, "hardwareModel", hardwareModel, "versionTarget", versionTarget)()
	}

	repo, err := NewRepository(repository)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("create ORAS repository client: %w", err)
	}

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
		Version:         selected.versionRaw,
		VersionString:   selected.versionString,
		Digest:          selected.payloadDigest,
		OCIReference:    fmt.Sprintf("%s:%s", repository, selected.tag),
		PayloadFilename: selected.payloadFilename,
	}, nil
}

func ResolvePayloadFromInventory(ctx context.Context, repository string, targets []string, model, softwareID, installedVersion string) (DiscoveryResult, bool, error) {
	if debug.IsEnabled() {
		defer debug.Trace("firmwareproxy.ResolvePayloadFromInventory", "repository", repository, "targets", targets, "model", model, "softwareID", softwareID, "installedVersion", installedVersion)()
	}

	repo, err := NewRepository(repository)
	if err != nil {
		return DiscoveryResult{}, false, fmt.Errorf("create ORAS repository client: %w", err)
	}

	var tags []string
	if err := repo.Tags(ctx, "", func(batch []string) error {
		tags = append(tags, batch...)
		return nil
	}); err != nil {
		return DiscoveryResult{}, false, classifyORASError(fmt.Errorf("list tags for %q: %w", repository, err))
	}

	if len(tags) == 0 {
		return DiscoveryResult{}, false, &HTTPStatusError{StatusCode: 404, Message: fmt.Sprintf("no tags found in repository %q", repository)}
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

		candidate, ok := buildManifestCandidateForInventory(manifest, tag, inventoryIdentity{
			targets:    targets,
			model:      model,
			softwareID: softwareID,
		})
		if !ok {
			continue
		}
		candidates = append(candidates, candidate)
	}

	selected, err := selectManifestCandidate(candidates, "latest")
	if err != nil {
		return DiscoveryResult{}, false, err
	}

	payloadIndex.Store(selected.payloadDigest, payloadLocation{Repository: repository})

	return DiscoveryResult{
		Version:         selected.versionRaw,
		VersionString:   selected.versionString,
		Digest:          selected.payloadDigest,
		OCIReference:    fmt.Sprintf("%s:%s", repository, selected.tag),
		PayloadFilename: selected.payloadFilename,
	}, true, nil
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
		versionString:     strings.TrimSpace(manifest.Annotations[annotationFirmwareVersionString]),
		versionNormalized: versionNormalized,
		payloadDigest:     manifest.Layers[0].Digest.String(),
		payloadFilename:   strings.TrimSpace(manifest.Layers[0].Annotations[annotationImageTitle]),
	}, true
}

func buildManifestCandidateForInventory(manifest ocispec.Manifest, tag string, identity inventoryIdentity) (manifestCandidate, bool) {
	if manifest.ArtifactType != FirmwareBundleArtifactType {
		return manifestCandidate{}, false
	}

	if len(manifest.Layers) == 0 {
		return manifestCandidate{}, false
	}

	if !isCompatibleHardwareAny(manifest.Annotations[annotationCompatibleHardware], identity.targets) {
		return manifestCandidate{}, false
	}

	if strings.TrimSpace(identity.softwareID) != "" {
		if !isCompatibleHardware(manifest.Annotations[annotationFirmwareSoftwareID], identity.softwareID) {
			return manifestCandidate{}, false
		}
	} else if !isCompatibleHardware(manifest.Annotations[annotationFirmwareModels], identity.model) {
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
		versionString:     strings.TrimSpace(manifest.Annotations[annotationFirmwareVersionString]),
		versionNormalized: versionNormalized,
		payloadDigest:     manifest.Layers[0].Digest.String(),
		payloadFilename:   strings.TrimSpace(manifest.Layers[0].Annotations[annotationImageTitle]),
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

func selectNewerManifestCandidate(candidates []manifestCandidate, installedVersion string) (manifestCandidate, bool, error) {
	if len(candidates) == 0 {
		return manifestCandidate{}, false, &HTTPStatusError{StatusCode: 404, Message: "no compatible firmware manifests found"}
	}

	sortManifestCandidates(candidates)

	installedNormalized, ok := normalizeComparableVersion(installedVersion)
	if !ok {
		return candidates[0], true, nil
	}

	for _, candidate := range candidates {
		if semver.Compare(candidate.versionNormalized, installedNormalized) > 0 {
			return candidate, true, nil
		}
	}

	return candidates[0], false, nil
}

// VersionsMatch reports whether registry and Redfish firmware version strings match.
func VersionsMatch(registryVersion, redfishVersion string) bool {
	return strings.TrimSpace(registryVersion) == strings.TrimSpace(redfishVersion)
}

func sortManifestCandidates(candidates []manifestCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		cmp := semver.Compare(candidates[i].versionNormalized, candidates[j].versionNormalized)
		if cmp == 0 {
			return candidates[i].tag < candidates[j].tag
		}
		return cmp > 0
	})
}

func normalizeSemver(version string) (string, bool) {
	return semverutil.NormalizeSemverCandidate(version)
}

func normalizeComparableVersion(version string) (string, bool) {
	return semverutil.NormalizeComparableSemver(version)
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

func isCompatibleHardwareAny(compatibilityAnnotation string, hardwareHints []string) bool {
	for _, hint := range hardwareHints {
		if isCompatibleHardware(compatibilityAnnotation, hint) {
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

	repo, err := NewRepository(loc.Repository)
	if err != nil {
		return nil, 0, fmt.Errorf("create ORAS repository client: %w", err)
	}

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

func classifyORASError(err error) error {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())

	if strings.Contains(msg, "status code 404") {
		return &HTTPStatusError{StatusCode: 404, Message: err.Error()}
	}

	if strings.Contains(msg, "status code 400") ||
		strings.Contains(msg, "status code 401") ||
		strings.Contains(msg, "status code 403") ||
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
	useInsecureTLS := repositoryInsecureTLS()

	if username == "" || password == "" {
		if !useInsecureTLS {
			return
		}

		repo.Client = &auth.Client{
			Client: insecureRegistryHTTPClient,
			Cache:  auth.NewCache(),
		}
		return
	}

	httpClient := http.DefaultClient
	if useInsecureTLS {
		httpClient = insecureRegistryHTTPClient
	}

	client := &auth.Client{
		Client: httpClient,
		Credential: auth.StaticCredential(repo.Reference.Registry, auth.Credential{
			Username: username,
			Password: password,
		}),
		Cache: auth.NewCache(),
	}
	repo.Client = client
}

func repositoryInsecureTLS() bool {
	value := strings.TrimSpace(os.Getenv(envRepositoryInsecureTLS))
	if value == "" {
		return false
	}

	parsed, err := strconv.ParseBool(value)
	if err == nil {
		return parsed
	}

	return false
}
