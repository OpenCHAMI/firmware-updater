// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/openchami/fabrica/pkg/fabrica"
)

// DeviceProfile is a Fabrica resource that captures all per-vendor Redfish
// behaviors needed to identify a device and perform a firmware update.
type DeviceProfile struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   fabrica.Metadata    `json:"metadata"`
	Spec       DeviceProfileSpec   `json:"spec" validate:"required"`
	Status     DeviceProfileStatus `json:"status,omitempty"`
}

// DeviceProfileSpec holds all profile configuration.
type DeviceProfileSpec struct {
	// ProfileID is the stable, human-readable identifier used for lookup
	// (e.g. "crayex", "ilo"). Must be unique across all loaded profiles.
	ProfileID string `json:"profileID" validate:"required" yaml:"id"`

	// Name is a human-readable display name.
	Name string `json:"name" yaml:"name"`

	// Enabled controls whether this profile is considered during matching.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// ----- Update dispatch -----

	// UpdateActionURI is the Redfish action path (absolute or relative).
	UpdateActionURI string `json:"updateActionURI" validate:"required" yaml:"updateActionURI"`

	// UpdatePayloadTemplate is a JSON template with %placeholder% tokens.
	UpdatePayloadTemplate string `json:"updatePayloadTemplate" validate:"required" yaml:"updatePayloadTemplate"`

	// UpdateMethod is the HTTP method; defaults to POST.
	UpdateMethod string `json:"updateMethod" yaml:"updateMethod"`

	// ----- Device identity -----

	ManufacturerPath  string `json:"manufacturerPath" validate:"required" yaml:"manufacturerPath"`
	ManufacturerField string `json:"manufacturerField" validate:"required" yaml:"manufacturerField"`
	ModelPath         string `json:"modelPath" validate:"required" yaml:"modelPath"`
	ModelField        string `json:"modelField" validate:"required" yaml:"modelField"`

	// SupportsInventoryExpand indicates the device accepts an OData expand query
	// appended to the FirmwareInventory URI discovered from the UpdateService link.
	// When true, the service fetches inventory in a single call; when false it
	// reads each member URI individually.
	// The FirmwareInventory URI itself is always discovered at runtime via:
	//   GET /redfish/v1/ → UpdateService["@odata.id"] → FirmwareInventory["@odata.id"]
	SupportsInventoryExpand bool `json:"supportsInventoryExpand" yaml:"supportsInventoryExpand"`

	// FirmwareInventoryExpandParam is the OData query string appended to the
	// FirmwareInventory URI when SupportsInventoryExpand is true.
	// Common values:
	//   "?$expand=."            – OData standard (default)
	//   "?$expand=*($levels=1)" – Dell iDRAC
	//   "?$expand=*"            – some AMI/Supermicro builds
	// Defaults to "?$expand=." when empty.
	FirmwareInventoryExpandParam string `json:"firmwareInventoryExpandParam" yaml:"firmwareInventoryExpandParam"`

	// Verification describes how to probe a device to confirm this profile applies.
	Verification VerificationSpec `json:"verification" yaml:"verification"`
}

// VerificationSpec defines the Redfish probe used for profile selection.
type VerificationSpec struct {
	Path    string `json:"path" yaml:"path"`
	Field   string `json:"field" yaml:"field"`
	Pattern string `json:"pattern" yaml:"pattern"`
}

// DeviceProfileStatus records how and when a profile was loaded.
type DeviceProfileStatus struct {
	// SourceFile is the filesystem path the profile was loaded from.
	// Empty when the profile was created directly via the API.
	SourceFile string `json:"sourceFile,omitempty"`

	// LoadedAt is an RFC3339 timestamp of the most recent load/upsert.
	LoadedAt string `json:"loadedAt,omitempty"`
}

var validProfileID = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Validate implements Fabrica's validation interface.
func (p *DeviceProfile) Validate(ctx context.Context) error {
	if strings.TrimSpace(p.Spec.ProfileID) == "" {
		return fmt.Errorf("spec.profileID is required")
	}
	if !validProfileID.MatchString(p.Spec.ProfileID) {
		return fmt.Errorf("spec.profileID must match [a-z0-9_-]+")
	}
	if !strings.HasPrefix(p.Spec.UpdateActionURI, "/") {
		return fmt.Errorf("spec.updateActionURI must start with /")
	}
	if p.Spec.Verification.Pattern != "" {
		if _, err := regexp.Compile(p.Spec.Verification.Pattern); err != nil {
			return fmt.Errorf("spec.verification.pattern is not a valid regexp: %w", err)
		}
	}
	return nil
}
