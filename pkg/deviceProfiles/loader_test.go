// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package deviceProfiles

import (
	"os"
	"path/filepath"
	"testing"
)

const validProfileYAML = `
id: crayex
name: "HPE Cray EX"
enabled: true
updateActionURI: /redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate
updatePayloadTemplate: |
  {
    "ImageURI": "%imageURI%"
  }
manufacturerPath: /redfish/v1/Chassis/Enclosure
manufacturerField: Manufacturer
modelPath: /redfish/v1/Chassis/Enclosure
modelField: Model
supportsInventoryExpand: false
verification:
  path: /redfish/v1/UpdateService/FirmwareInventory/BMC
  field: SoftwareId
  pattern: "^(nc|cc|sc)"
`

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestLoadFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "crayex.yaml", validProfileYAML)

	profile, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}

	if profile.APIVersion != "hardware.fabrica.dev/v1" {
		t.Errorf("APIVersion = %q, want hardware.fabrica.dev/v1", profile.APIVersion)
	}
	if profile.Kind != "DeviceProfile" {
		t.Errorf("Kind = %q, want DeviceProfile", profile.Kind)
	}
	if profile.Spec.ProfileID != "crayex" {
		t.Errorf("ProfileID = %q, want crayex", profile.Spec.ProfileID)
	}
	if profile.Metadata.Name != "crayex" {
		t.Errorf("Metadata.Name = %q, want crayex", profile.Metadata.Name)
	}
	// Default UpdateMethod should be applied.
	if profile.Spec.UpdateMethod != "POST" {
		t.Errorf("UpdateMethod = %q, want POST", profile.Spec.UpdateMethod)
	}
}

func TestLoadFile_ExpandDefault(t *testing.T) {
	dir := t.TempDir()
	yaml := `
id: ilo
name: "HPE iLO"
enabled: true
updateActionURI: /redfish/v1/UpdateService/Actions/SimpleUpdate
updatePayloadTemplate: |
  {"ImageURI": "%imageURI%"}
manufacturerPath: /redfish/v1/Chassis/1
manufacturerField: Manufacturer
modelPath: /redfish/v1/Chassis/1
modelField: Model
supportsInventoryExpand: true
verification:
  path: /redfish/v1/Managers/1
  field: Model
  pattern: "^iLO"
`
	path := writeTemp(t, dir, "ilo.yaml", yaml)

	profile, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}
	if profile.Spec.FirmwareInventoryExpandParam != "?$expand=." {
		t.Errorf("FirmwareInventoryExpandParam = %q, want '?$expand=.'", profile.Spec.FirmwareInventoryExpandParam)
	}
}

func TestLoadFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "bad.yaml", "id: [unterminated\n")

	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoadFile_MissingRequiredField(t *testing.T) {
	dir := t.TempDir()
	// Missing updateActionURI -> Validate should fail.
	yaml := `
id: broken
name: "Broken"
enabled: true
`
	path := writeTemp(t, dir, "broken.yaml", yaml)

	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected validation error for missing updateActionURI, got nil")
	}
}

func TestLoadFile_BadRegex(t *testing.T) {
	dir := t.TempDir()
	yaml := `
id: badregex
name: "Bad Regex"
enabled: true
updateActionURI: /redfish/v1/UpdateService/Actions/SimpleUpdate
updatePayloadTemplate: |
  {"ImageURI": "%imageURI%"}
manufacturerPath: /redfish/v1/Chassis/1
manufacturerField: Manufacturer
modelPath: /redfish/v1/Chassis/1
modelField: Model
verification:
  path: /redfish/v1/Managers/1
  field: Model
  pattern: "[invalid("
`
	path := writeTemp(t, dir, "badregex.yaml", yaml)

	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected error for invalid regex pattern, got nil")
	}
}

func TestLoadDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "crayex.yaml", validProfileYAML)
	writeTemp(t, dir, "notes.txt", "ignore me") // non-YAML file should be skipped

	reg := NewRegistry()
	errs := LoadDirectory(dir, reg)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if got := len(reg.List()); got != 1 {
		t.Fatalf("expected 1 profile loaded, got %d", got)
	}

	loaded, _ := reg.Get("crayex")
	if loaded.Status.SourceFile == "" {
		t.Error("expected Status.SourceFile to be set")
	}
	if loaded.Status.LoadedAt == "" {
		t.Error("expected Status.LoadedAt to be set")
	}
}

func TestLoadDirectory_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.yaml", validProfileYAML)
	writeTemp(t, dir, "b.yaml", validProfileYAML) // same id: crayex

	reg := NewRegistry()
	errs := LoadDirectory(dir, reg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 duplicate error, got %d: %v", len(errs), errs)
	}
	if got := len(reg.List()); got != 1 {
		t.Fatalf("expected 1 profile registered, got %d", got)
	}
}

func TestLoadDirectory_MissingDir(t *testing.T) {
	reg := NewRegistry()
	errs := LoadDirectory(filepath.Join(t.TempDir(), "does-not-exist"), reg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for missing directory, got %d", len(errs))
	}
}
