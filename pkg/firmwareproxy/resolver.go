package firmwareproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

const FirmwareBundleArtifactType = "application/vnd.openchami.firmware.bundle.v1+json"

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

var payloadIndex sync.Map

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
