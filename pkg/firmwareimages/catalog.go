package firmwareimages

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/user/firmware-updater/pkg/debug"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
	"github.com/user/firmware-updater/pkg/semverutil"
	"golang.org/x/mod/semver"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"
)

// ListFirmwareImages returns firmware bundle manifests from a repository or registry.
func ListFirmwareImages(ctx context.Context, registryHost, repository string) ([]FirmwareImageMetadata, error) {
	if debug.IsEnabled() {
		defer debug.Trace("firmwareimages.ListFirmwareImages", "registryHost", registryHost, "repository", repository)()
	}

	repositories, err := repositoriesForList(ctx, registryHost, repository)
	if err != nil {
		return nil, err
	}

	var images []FirmwareImageMetadata
	for _, repositoryName := range repositories {
		repo, err := firmwareproxy.NewRepository(repositoryName)
		if err != nil {
			return nil, fmt.Errorf("create OCI repository client: %w", err)
		}

		var tags []string
		if err := repo.Tags(ctx, "", func(batch []string) error {
			tags = append(tags, batch...)
			return nil
		}); err != nil {
			return nil, mapRegistryError(fmt.Errorf("list tags for %q: %w", repositoryName, err))
		}

		for _, tag := range tags {
			image, found, err := getFirmwareImage(ctx, repo, repositoryName, tag)
			if err != nil {
				return nil, err
			}
			if found {
				images = append(images, image)
			}
		}
	}

	sort.Slice(images, func(i, j int) bool {
		if images[i].Repository == images[j].Repository {
			return images[i].Tag < images[j].Tag
		}
		return images[i].Repository < images[j].Repository
	})
	return images, nil
}

// GetFirmwareImage returns one firmware bundle selected by repository and tag.
func GetFirmwareImage(ctx context.Context, repository, tag string) (FirmwareImageMetadata, error) {
	if debug.IsEnabled() {
		defer debug.Trace("firmwareimages.GetFirmwareImage", "repository", repository, "tag", tag)()
	}

	if strings.TrimSpace(repository) == "" || strings.TrimSpace(tag) == "" {
		return FirmwareImageMetadata{}, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "repository and tag are required"}
	}
	repo, err := firmwareproxy.NewRepository(repository)
	if err != nil {
		return FirmwareImageMetadata{}, fmt.Errorf("create OCI repository client: %w", err)
	}
	image, found, err := getFirmwareImage(ctx, repo, repository, tag)
	if err != nil {
		return FirmwareImageMetadata{}, err
	}
	if !found {
		return FirmwareImageMetadata{}, &firmwareproxy.HTTPStatusError{StatusCode: 404, Message: "firmware image not found"}
	}
	return image, nil
}

// SearchFirmwareImages returns every image that matches every supplied filter.
// When latest is true, it returns only the image with the highest semantic version.
func SearchFirmwareImages(ctx context.Context, registryHost, repository string, filters map[string]string, latest bool) ([]FirmwareImageMetadata, error) {
	if debug.IsEnabled() {
		defer debug.Trace("firmwareimages.SearchFirmwareImages", "registryHost", registryHost, "repository", repository, "latest", latest)()
	}

	if len(filters) == 0 && !latest {
		return nil, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "at least one search filter is required"}
	}
	images, err := ListFirmwareImages(ctx, registryHost, repository)
	if err != nil {
		return nil, err
	}
	var matches []FirmwareImageMetadata
	for _, image := range images {
		if matchesFilters(image, filters) {
			matches = append(matches, image)
		}
	}
	if !latest {
		return matches, nil
	}
	return latestFirmwareImage(matches), nil
}

func latestFirmwareImage(images []FirmwareImageMetadata) []FirmwareImageMetadata {
	var latest FirmwareImageMetadata
	latestVersion := ""
	for _, image := range images {
		normalized, ok := semverutil.NormalizeSemverCandidate(image.Version)
		if !ok {
			continue
		}
		if latestVersion == "" || semver.Compare(normalized, latestVersion) > 0 || (semver.Compare(normalized, latestVersion) == 0 && image.Tag < latest.Tag) {
			latest = image
			latestVersion = normalized
		}
	}
	if latestVersion == "" {
		return []FirmwareImageMetadata{}
	}
	return []FirmwareImageMetadata{latest}
}

// DeleteFirmwareImage deletes a tag-selected firmware manifest. Shared manifests require force.
func DeleteFirmwareImage(ctx context.Context, repository, tag string, force bool) error {
	image, err := GetFirmwareImage(ctx, repository, tag)
	if err != nil {
		return err
	}
	repo, err := firmwareproxy.NewRepository(repository)
	if err != nil {
		return fmt.Errorf("create OCI repository client: %w", err)
	}

	var tags []string
	if err := repo.Tags(ctx, "", func(batch []string) error {
		tags = append(tags, batch...)
		return nil
	}); err != nil {
		return mapRegistryError(fmt.Errorf("list tags for %q: %w", repository, err))
	}

	var sharedTags []string
	for _, candidateTag := range tags {
		if candidateTag == tag {
			continue
		}
		descriptor, err := repo.Resolve(ctx, candidateTag)
		if err == nil && descriptor.Digest.String() == image.Digest {
			sharedTags = append(sharedTags, candidateTag)
		}
	}
	if len(sharedTags) > 0 && !force {
		sort.Strings(sharedTags)
		return &firmwareproxy.HTTPStatusError{StatusCode: 409, Message: "firmware manifest is also referenced by tags: " + strings.Join(sharedTags, ", ")}
	}

	descriptor, err := repo.Resolve(ctx, tag)
	if err != nil {
		return mapRegistryError(fmt.Errorf("resolve firmware tag %q: %w", tag, err))
	}
	if err := repo.Delete(ctx, descriptor); err != nil {
		return mapRegistryError(fmt.Errorf("delete firmware manifest: %w", err))
	}
	return nil
}

func repositoriesForList(ctx context.Context, registryHost, repository string) ([]string, error) {
	if repository = strings.TrimSpace(repository); repository != "" {
		return []string{repository}, nil
	}
	if registryHost = strings.TrimSpace(registryHost); registryHost == "" {
		return nil, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "registry host is required when repository is not specified"}
	}
	registryClient, err := firmwareproxy.NewRegistry(registryHost)
	if err != nil {
		return nil, fmt.Errorf("create OCI registry client: %w", err)
	}
	var repositories []string
	if err := registryClient.Repositories(ctx, "", func(batch []string) error {
		for _, name := range batch {
			repositories = append(repositories, registryHost+"/"+name)
		}
		return nil
	}); err != nil {
		return nil, mapRegistryError(fmt.Errorf("list OCI repositories: %w", err))
	}
	return repositories, nil
}

func getFirmwareImage(ctx context.Context, repo *remote.Repository, repository, tag string) (FirmwareImageMetadata, bool, error) {
	descriptor, manifestBytes, err := oras.FetchBytes(ctx, repo, tag, oras.FetchBytesOptions{})
	if err != nil {
		if isNotFound(err) {
			return FirmwareImageMetadata{}, false, nil
		}
		return FirmwareImageMetadata{}, false, mapRegistryError(fmt.Errorf("fetch firmware manifest: %w", err))
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return FirmwareImageMetadata{}, false, fmt.Errorf("decode OCI manifest: %w", err)
	}
	if manifest.ArtifactType != firmwareBundleArtifactType || len(manifest.Layers) == 0 {
		return FirmwareImageMetadata{}, false, nil
	}
	layer := manifest.Layers[0]
	return FirmwareImageMetadata{
		Repository:    repository,
		Tag:           tag,
		Digest:        descriptor.Digest.String(),
		Version:       strings.TrimSpace(manifest.Annotations[annotationImageVersion]),
		Filename:      strings.TrimSpace(layer.Annotations[annotationImageTitle]),
		Targets:       splitAnnotation(manifest.Annotations[annotationCompatibleHardware]),
		Models:        splitAnnotation(manifest.Annotations[annotationModels]),
		SoftwareIDs:   splitAnnotation(manifest.Annotations[annotationSoftwareIDs]),
		VersionString: strings.TrimSpace(manifest.Annotations[annotationVersionString]),
		Manufacturer:  strings.TrimSpace(manifest.Annotations[annotationManufacturer]),
		DeviceType:    strings.TrimSpace(manifest.Annotations[annotationDeviceType]),
		Tags:          splitAnnotation(manifest.Annotations[annotationFirmwareTags]),
		SizeBytes:     layer.Size,
	}, true, nil
}

func matchesFilters(image FirmwareImageMetadata, filters map[string]string) bool {
	for key, value := range filters {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch key {
		case "manufacturer":
			if !strings.EqualFold(image.Manufacturer, value) {
				return false
			}
		case "deviceType":
			if !strings.EqualFold(image.DeviceType, value) {
				return false
			}
		case "version":
			if !strings.EqualFold(image.Version, value) {
				return false
			}
		case "filename":
			if !strings.Contains(strings.ToLower(image.Filename), strings.ToLower(value)) {
				return false
			}
		case "model":
			if !containsSubstring(image.Models, value) {
				return false
			}
		case "target":
			if !containsSubstring(image.Targets, value) {
				return false
			}
		case "softwareId":
			if !containsSubstring(image.SoftwareIDs, value) {
				return false
			}
		case "versionString":
			if !strings.Contains(strings.ToLower(image.VersionString), strings.ToLower(value)) {
				return false
			}
		case "tag":
			if !containsSubstring(image.Tags, value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func containsSubstring(values []string, query string) bool {
	query = strings.ToLower(query)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func mapRegistryError(err error) error {
	if isNotFound(err) {
		return &firmwareproxy.HTTPStatusError{StatusCode: 404, Message: err.Error()}
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "status code 401") || strings.Contains(message, "status code 403") || strings.Contains(message, "status code 400") {
		return &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: err.Error()}
	}
	return &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: err.Error()}
}

func isNotFound(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "status code 404")
}

// ParseForce converts the optional force query parameter to a boolean.
func ParseForce(value string) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return false, nil
	}
	force, err := strconv.ParseBool(value)
	if err != nil {
		return false, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "force must be true or false"}
	}
	return force, nil
}

// ParseLatest converts the optional latest query parameter to a boolean.
func ParseLatest(value string) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return false, nil
	}
	latest, err := strconv.ParseBool(value)
	if err != nil {
		return false, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "latest must be true or false"}
	}
	return latest, nil
}
