// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"testing"
)

func baseValidProfile() *DeviceProfile {
	return &DeviceProfile{
		APIVersion: "hardware.fabrica.dev/v1",
		Kind:       "DeviceProfile",
		Spec: DeviceProfileSpec{
			ProfileID:             "crayex",
			Name:                  "HPE Cray EX",
			Enabled:               true,
			UpdateActionURI:       "/redfish/v1/UpdateService/Actions/SimpleUpdate",
			UpdatePayloadTemplate: `{"ImageURI": "%imageURI%"}`,
			ManufacturerPath:      "/redfish/v1/Chassis/Enclosure",
			ManufacturerField:     "Manufacturer",
			ModelPath:             "/redfish/v1/Chassis/Enclosure",
			ModelField:            "Model",
			Verification: VerificationSpec{
				Path:    "/redfish/v1/Managers/1",
				Field:   "Model",
				Pattern: "^iLO",
			},
		},
	}
}

func TestValidate_Valid(t *testing.T) {
	if err := baseValidProfile().Validate(context.Background()); err != nil {
		t.Fatalf("expected valid profile, got error: %v", err)
	}
}

func TestValidate_MissingProfileID(t *testing.T) {
	p := baseValidProfile()
	p.Spec.ProfileID = ""
	if err := p.Validate(context.Background()); err == nil {
		t.Fatal("expected error for empty ProfileID")
	}
}

func TestValidate_InvalidProfileIDChars(t *testing.T) {
	for _, id := range []string{"UPPER", "has space", "dot.dot", "at@sign"} {
		p := baseValidProfile()
		p.Spec.ProfileID = id
		if err := p.Validate(context.Background()); err == nil {
			t.Errorf("expected error for invalid ProfileID %q", id)
		}
	}
}

func TestValidate_ValidProfileIDChars(t *testing.T) {
	for _, id := range []string{"a", "crayex", "ilo-6", "amd_mi300", "x1-y_2"} {
		p := baseValidProfile()
		p.Spec.ProfileID = id
		if err := p.Validate(context.Background()); err != nil {
			t.Errorf("expected valid ProfileID %q, got error: %v", id, err)
		}
	}
}

func TestValidate_UpdateActionURIMustStartWithSlash(t *testing.T) {
	p := baseValidProfile()
	p.Spec.UpdateActionURI = "redfish/v1/UpdateService/Actions/SimpleUpdate"
	if err := p.Validate(context.Background()); err == nil {
		t.Fatal("expected error for updateActionURI not starting with /")
	}
}

func TestValidate_BadVerificationPattern(t *testing.T) {
	p := baseValidProfile()
	p.Spec.Verification.Pattern = "[invalid("
	if err := p.Validate(context.Background()); err == nil {
		t.Fatal("expected error for invalid verification pattern")
	}
}

func TestValidate_EmptyVerificationPatternAllowed(t *testing.T) {
	p := baseValidProfile()
	p.Spec.Verification.Pattern = ""
	if err := p.Validate(context.Background()); err != nil {
		t.Fatalf("empty pattern should be allowed, got error: %v", err)
	}
}
