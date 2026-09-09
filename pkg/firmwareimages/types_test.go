package firmwareimages

import "testing"

func TestMatchesFiltersRequiresEveryFilter(t *testing.T) {
	image := FirmwareImageMetadata{
		Manufacturer:  "hpe",
		DeviceType:    "nodeBMC",
		Version:       "3.0.1",
		VersionString: "3.0.1 build 42",
		Filename:      "ilo-firmware.bin",
		Targets:       []string{"iLO 5"},
		Models:        []string{"iLO 5"},
		SoftwareIDs:   []string{"ilo5"},
		Tags:          []string{"default", "production"},
	}

	if !matchesFilters(image, map[string]string{"manufacturer": "HPE", "model": "ilo", "target": "ilo", "softwareId": "ilo5", "versionString": "BUILD", "tag": "prod"}) {
		t.Fatal("expected all matching filters to match")
	}
	if matchesFilters(image, map[string]string{"manufacturer": "hpe", "version": "9.9.9"}) {
		t.Fatal("expected non-matching version to reject image")
	}
	if matchesFilters(image, map[string]string{"unknown": "value"}) {
		t.Fatal("expected unknown filter to reject image")
	}
	if matchesFilters(image, map[string]string{"softwareId": "missing"}) {
		t.Fatal("expected non-matching software ID to reject image")
	}
}

func TestParseForce(t *testing.T) {
	force, err := ParseForce("true")
	if err != nil || !force {
		t.Fatalf("expected true force value, got %t and %v", force, err)
	}
	if _, err := ParseForce("invalid"); err == nil {
		t.Fatal("expected invalid force value to fail")
	}
}

func TestLatestFirmwareImage(t *testing.T) {
	images := []FirmwareImageMetadata{
		{Tag: "v1", Version: "1.2.0"},
		{Tag: "v2", Version: "1.10.0"},
		{Tag: "invalid", Version: "not-a-version"},
	}

	latest := latestFirmwareImage(images)
	if len(latest) != 1 || latest[0].Tag != "v2" {
		t.Fatalf("expected v2 to be the latest image, got %#v", latest)
	}
	if latest := latestFirmwareImage([]FirmwareImageMetadata{{Tag: "bad", Version: "unknown"}}); len(latest) != 0 {
		t.Fatalf("expected no latest image for invalid version, got %#v", latest)
	}
}

func TestParseLatest(t *testing.T) {
	latest, err := ParseLatest("true")
	if err != nil || !latest {
		t.Fatalf("expected true latest value, got %t and %v", latest, err)
	}
	if _, err := ParseLatest("invalid"); err == nil {
		t.Fatal("expected invalid latest value to fail")
	}
}

func TestManifestAnnotations(t *testing.T) {
	annotations := manifestAnnotations(PushRequest{
		Version:       "1.2.3",
		Targets:       []string{" iLO 5 ", "iLO 6"},
		Models:        []string{"ProLiant DL325", "ProLiant DL385"},
		SoftwareIDs:   []string{"ilo5", "ilo6"},
		VersionString: "2.95 Jul 19 2023",
		Tags:          []string{"default", "production"},
	})
	if annotations[annotationCompatibleHardware] != "iLO 5,iLO 6" {
		t.Fatalf("unexpected compatible hardware annotation: %q", annotations[annotationCompatibleHardware])
	}
	if annotations[annotationFirmwareTags] != "default,production" {
		t.Fatalf("unexpected tag annotation: %q", annotations[annotationFirmwareTags])
	}
	if annotations[annotationModels] != "ProLiant DL325,ProLiant DL385" {
		t.Fatalf("unexpected models annotation: %q", annotations[annotationModels])
	}
	if annotations[annotationSoftwareIDs] != "ilo5,ilo6" {
		t.Fatalf("unexpected software IDs annotation: %q", annotations[annotationSoftwareIDs])
	}
	if annotations[annotationVersionString] != "2.95 Jul 19 2023" {
		t.Fatalf("unexpected version string annotation: %q", annotations[annotationVersionString])
	}
}

func TestFabricaResourceShape(t *testing.T) {
	resource := FabricaResource(FirmwareImageMetadata{
		Repository: "registry:5000/firmware/ilo",
		Tag:        "3.0.1",
		Digest:     "sha256:abc123",
		Version:    "3.0.1",
		Targets:    []string{"iLO 5"},
		SizeBytes:  42,
	})

	if resource.APIVersion != "hardware.fabrica.dev/v1" || resource.Kind != "FirmwareImage" {
		t.Fatalf("unexpected Fabrica identity: %#v", resource)
	}
	if resource.Spec.Repository != "registry:5000/firmware/ilo" || resource.Spec.Targets[0] != "iLO 5" {
		t.Fatalf("unexpected Fabrica spec: %#v", resource.Spec)
	}
	if resource.Status.Digest != "sha256:abc123" || resource.Status.SizeBytes != 42 {
		t.Fatalf("unexpected Fabrica status: %#v", resource.Status)
	}
}
