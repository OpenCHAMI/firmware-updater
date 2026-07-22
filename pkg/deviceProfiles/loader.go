// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package deviceProfiles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"gopkg.in/yaml.v3"
)

// LoadDirectory reads all *.yaml and *.yml files from dir, parses each into a
// DeviceProfile, and registers each in reg. It returns one error per file that
// fails to load; failures are non-fatal so a bad file does not block others.
func LoadDirectory(dir string, reg *Registry) []error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []error{fmt.Errorf("read device profiles directory %s: %w", dir, err)}
	}

	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		profile, err := LoadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("load %s: %w", path, err))
			continue
		}

		profile.Status.SourceFile = path
		profile.Status.LoadedAt = time.Now().UTC().Format(time.RFC3339)

		if err := reg.Register(profile); err != nil {
			errs = append(errs, fmt.Errorf("register %s: %w", path, err))
		}
	}

	return errs
}

// LoadFile reads, parses, applies defaults to, and validates a single profile
// YAML file, returning the resulting DeviceProfile.
func LoadFile(path string) (v1.DeviceProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return v1.DeviceProfile{}, fmt.Errorf("read file: %w", err)
	}

	// The YAML fields map directly onto DeviceProfileSpec via its yaml tags.
	var spec v1.DeviceProfileSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return v1.DeviceProfile{}, fmt.Errorf("parse YAML: %w", err)
	}

	applyDefaults(&spec)

	profile := v1.DeviceProfile{
		APIVersion: "hardware.fabrica.dev/v1",
		Kind:       "DeviceProfile",
		Spec:       spec,
	}
	profile.Metadata.Name = spec.ProfileID

	if err := profile.Validate(context.Background()); err != nil {
		return v1.DeviceProfile{}, fmt.Errorf("validate: %w", err)
	}

	return profile, nil
}

// applyDefaults fills in default values for optional fields left unset in YAML.
func applyDefaults(spec *v1.DeviceProfileSpec) {
	if spec.UpdateMethod == "" {
		spec.UpdateMethod = "POST"
	}
	if spec.SupportsInventoryExpand && spec.FirmwareInventoryExpandParam == "" {
		spec.FirmwareInventoryExpandParam = "?$expand=."
	}
}
