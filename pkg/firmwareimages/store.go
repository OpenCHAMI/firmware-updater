package firmwareimages

import (
	"context"
	"fmt"
	"io"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"
)

// PushFirmwareImage stores a firmware bundle at the supplied OCI repository and tag.
func PushFirmwareImage(ctx context.Context, request PushRequest, payload io.Reader, filename string) (FirmwareImageMetadata, error) {
	if err := validatePushRequest(request, filename); err != nil {
		return FirmwareImageMetadata{}, err
	}

	payloadBytes, err := io.ReadAll(payload)
	if err != nil {
		return FirmwareImageMetadata{}, fmt.Errorf("read firmware payload: %w", err)
	}

	repo, err := firmwareproxy.NewRepository(request.Repository)
	if err != nil {
		return FirmwareImageMetadata{}, mapRegistryError(fmt.Errorf("create OCI repository client: %w", err))
	}

	layer, err := oras.PushBytes(ctx, repo.Blobs(), "application/octet-stream", payloadBytes)
	if err != nil {
		return FirmwareImageMetadata{}, mapRegistryError(fmt.Errorf("push firmware payload: %w", err))
	}
	layer.Annotations = map[string]string{annotationImageTitle: strings.TrimSpace(filename)}

	manifest, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, firmwareBundleArtifactType, oras.PackManifestOptions{
		Layers:              []ocispec.Descriptor{layer},
		ManifestAnnotations: manifestAnnotations(request),
	})
	if err != nil {
		return FirmwareImageMetadata{}, mapRegistryError(fmt.Errorf("pack firmware manifest: %w", err))
	}
	if err := repo.Tag(ctx, manifest, request.Tag); err != nil {
		return FirmwareImageMetadata{}, mapRegistryError(fmt.Errorf("tag firmware manifest: %w", err))
	}

	return FirmwareImageMetadata{
		Repository:    request.Repository,
		Tag:           request.Tag,
		Digest:        manifest.Digest.String(),
		Version:       request.Version,
		Filename:      strings.TrimSpace(filename),
		Targets:       normalizedValues(request.Targets),
		Models:        normalizedValues(request.Models),
		SoftwareIDs:   normalizedValues(request.SoftwareIDs),
		VersionString: strings.TrimSpace(request.VersionString),
		Manufacturer:  strings.TrimSpace(request.Manufacturer),
		DeviceType:    strings.TrimSpace(request.DeviceType),
		Tags:          normalizedValues(request.Tags),
		SizeBytes:     int64(len(payloadBytes)),
	}, nil
}

func validatePushRequest(request PushRequest, filename string) error {
	if strings.TrimSpace(request.Repository) == "" || strings.TrimSpace(request.Tag) == "" || strings.TrimSpace(request.Version) == "" || strings.TrimSpace(filename) == "" || len(normalizedValues(request.Targets)) == 0 {
		return &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "repository, tag, version, targets, and file are required"}
	}
	parsed, err := registry.ParseReference(strings.TrimSpace(request.Repository) + ":" + strings.TrimSpace(request.Tag))
	if err != nil {
		return &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: fmt.Sprintf("invalid OCI repository or tag: %v", err)}
	}
	if parsed.Reference == "" {
		return &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "OCI tag is required"}
	}
	return nil
}

func manifestAnnotations(request PushRequest) map[string]string {
	return map[string]string{
		annotationImageVersion:       strings.TrimSpace(request.Version),
		annotationCompatibleHardware: joinAnnotation(request.Targets),
		annotationManufacturer:       strings.TrimSpace(request.Manufacturer),
		annotationDeviceType:         strings.TrimSpace(request.DeviceType),
		annotationFirmwareTags:       joinAnnotation(request.Tags),
		annotationModels:             joinAnnotation(request.Models),
		annotationSoftwareIDs:        joinAnnotation(request.SoftwareIDs),
		annotationVersionString:      strings.TrimSpace(request.VersionString),
	}
}

func normalizedValues(values []string) []string {
	return splitAnnotation(strings.Join(values, ","))
}
