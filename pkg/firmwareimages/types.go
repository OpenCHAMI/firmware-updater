package firmwareimages

import (
	"strings"
	"time"

	"github.com/openchami/fabrica/pkg/fabrica"
)

const (
	firmwareBundleArtifactType   = "application/vnd.openchami.firmware.bundle.v1+json"
	annotationCompatibleHardware = "dev.fabrica.hardware.compatible"
	annotationManufacturer       = "dev.fabrica.hardware.manufacturer"
	annotationDeviceType         = "dev.fabrica.hardware.deviceType"
	annotationFirmwareTags       = "dev.fabrica.firmware.tags"
	annotationModels             = "dev.fabrica.firmware.models"
	annotationSoftwareIDs        = "dev.fabrica.firmware.softwareIds"
	annotationVersionString      = "dev.fabrica.firmware.versionString"
	annotationImageVersion       = "org.opencontainers.image.version"
	annotationImageTitle         = "org.opencontainers.image.title"
)

// FirmwareImageMetadata describes a firmware artifact stored in an OCI registry.
type FirmwareImageMetadata struct {
	Repository    string   `json:"repository"`
	Tag           string   `json:"tag"`
	Digest        string   `json:"digest"`
	Version       string   `json:"version"`
	Filename      string   `json:"filename"`
	Targets       []string `json:"targets"`
	Models        []string `json:"models"`
	SoftwareIDs   []string `json:"softwareIds"`
	VersionString string   `json:"versionString"`
	Manufacturer  string   `json:"manufacturer,omitempty"`
	DeviceType    string   `json:"deviceType,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	SizeBytes     int64    `json:"sizeBytes"`
}

// PushRequest is the metadata supplied with a firmware image upload.
type PushRequest struct {
	Repository    string   `json:"repository"`
	Tag           string   `json:"tag"`
	Version       string   `json:"version"`
	Targets       []string `json:"targets"`
	Models        []string `json:"models"`
	SoftwareIDs   []string `json:"softwareIds"`
	VersionString string   `json:"versionString"`
	Manufacturer  string   `json:"manufacturer,omitempty"`
	DeviceType    string   `json:"deviceType,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// FirmwareImage is the Fabrica resource representation returned by the API.
type FirmwareImage struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   fabrica.Metadata    `json:"metadata"`
	Spec       FirmwareImageSpec   `json:"spec"`
	Status     FirmwareImageStatus `json:"status,omitempty"`
}

// FirmwareImageSpec contains the requested and searchable image metadata.
type FirmwareImageSpec struct {
	Repository    string   `json:"repository"`
	Tag           string   `json:"tag"`
	Version       string   `json:"version"`
	Filename      string   `json:"filename"`
	Targets       []string `json:"targets"`
	Models        []string `json:"models"`
	SoftwareIDs   []string `json:"softwareIds"`
	VersionString string   `json:"versionString"`
	Manufacturer  string   `json:"manufacturer,omitempty"`
	DeviceType    string   `json:"deviceType,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// FirmwareImageStatus contains the observed OCI manifest details.
type FirmwareImageStatus struct {
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"sizeBytes"`
}

// FabricaResource converts image metadata to the standard Fabrica resource shape.
func FabricaResource(image FirmwareImageMetadata) FirmwareImage {
	now := time.Now().UTC()
	return FirmwareImage{
		APIVersion: "hardware.fabrica.dev/v1",
		Kind:       "FirmwareImage",
		Metadata: fabrica.Metadata{
			Name:      image.Repository + ":" + image.Tag,
			UID:       image.Digest,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Spec: FirmwareImageSpec{
			Repository:    image.Repository,
			Tag:           image.Tag,
			Version:       image.Version,
			Filename:      image.Filename,
			Targets:       image.Targets,
			Models:        image.Models,
			SoftwareIDs:   image.SoftwareIDs,
			VersionString: image.VersionString,
			Manufacturer:  image.Manufacturer,
			DeviceType:    image.DeviceType,
			Tags:          image.Tags,
		},
		Status: FirmwareImageStatus{Digest: image.Digest, SizeBytes: image.SizeBytes},
	}
}

func splitAnnotation(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func joinAnnotation(values []string) string {
	return strings.Join(splitAnnotation(strings.Join(values, ",")), ",")
}
