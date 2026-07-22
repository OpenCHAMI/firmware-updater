// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package deviceProfiles

import (
	"testing"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
)

func testProfile(id string) v1.DeviceProfile {
	return v1.DeviceProfile{
		APIVersion: "hardware.fabrica.dev/v1",
		Kind:       "DeviceProfile",
		Spec: v1.DeviceProfileSpec{
			ProfileID:       id,
			Name:            "Test " + id,
			Enabled:         true,
			UpdateActionURI: "/redfish/v1/UpdateService/Actions/SimpleUpdate",
		},
	}
}

func TestRegistry_Register(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(testProfile("alpha")); err != nil {
		t.Fatalf("first register: unexpected error: %v", err)
	}

	if err := reg.Register(testProfile("alpha")); err == nil {
		t.Fatal("expected error registering duplicate ID, got nil")
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(testProfile("alpha"))

	got, ok := reg.Get("alpha")
	if !ok {
		t.Fatal("expected to find profile 'alpha'")
	}
	if got.Spec.ProfileID != "alpha" {
		t.Fatalf("expected ProfileID 'alpha', got %q", got.Spec.ProfileID)
	}

	if _, ok := reg.Get("missing"); ok {
		t.Fatal("expected not found for 'missing'")
	}
}

func TestRegistry_List(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(testProfile("alpha"))
	_ = reg.Register(testProfile("beta"))

	if got := len(reg.List()); got != 2 {
		t.Fatalf("expected 2 profiles, got %d", got)
	}
}

func TestRegistry_Delete(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(testProfile("alpha"))

	if !reg.Delete("alpha") {
		t.Fatal("expected Delete to return true for existing profile")
	}
	if reg.Delete("alpha") {
		t.Fatal("expected Delete to return false for already-removed profile")
	}
}

func TestRegistry_Upsert(t *testing.T) {
	reg := NewRegistry()

	p := testProfile("alpha")
	p.Spec.Name = "First"
	reg.Upsert(p)

	got, _ := reg.Get("alpha")
	if got.Spec.Name != "First" {
		t.Fatalf("expected name 'First', got %q", got.Spec.Name)
	}

	p.Spec.Name = "Second"
	reg.Upsert(p)

	got, _ = reg.Get("alpha")
	if got.Spec.Name != "Second" {
		t.Fatalf("expected name 'Second' after upsert, got %q", got.Spec.Name)
	}

	if got := len(reg.List()); got != 1 {
		t.Fatalf("expected 1 profile after upsert of same ID, got %d", got)
	}
}
