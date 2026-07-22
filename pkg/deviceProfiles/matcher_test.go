// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package deviceProfiles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
)

func TestBuildUpdatePayload_Substitution(t *testing.T) {
	p := v1.DeviceProfile{
		Spec: v1.DeviceProfileSpec{
			UpdatePayloadTemplate: `{"ImageURI": "%imageURI%", "Targets": ["%target%"]}`,
		},
	}

	out, err := BuildUpdatePayload(p, map[string]string{
		"imageURI": "http://proxy/layer/sha256:abc",
		"target":   "/redfish/v1/UpdateService/FirmwareInventory/BMC",
	})
	if err != nil {
		t.Fatalf("BuildUpdatePayload: unexpected error: %v", err)
	}

	var decoded struct {
		ImageURI string   `json:"ImageURI"`
		Targets  []string `json:"Targets"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if decoded.ImageURI != "http://proxy/layer/sha256:abc" {
		t.Errorf("ImageURI = %q", decoded.ImageURI)
	}
	if len(decoded.Targets) != 1 || decoded.Targets[0] != "/redfish/v1/UpdateService/FirmwareInventory/BMC" {
		t.Errorf("Targets = %v", decoded.Targets)
	}
}

func TestBuildUpdatePayload_UnresolvedPlaceholderInvalidJSON(t *testing.T) {
	p := v1.DeviceProfile{
		Spec: v1.DeviceProfileSpec{
			// Without the surrounding quotes being filled, this remains valid JSON,
			// so use a numeric placeholder to force invalid JSON when unresolved.
			UpdatePayloadTemplate: `{"Level": %level%}`,
		},
	}

	if _, err := BuildUpdatePayload(p, nil); err == nil {
		t.Fatal("expected error for unresolved numeric placeholder, got nil")
	}
}

func TestMatchDevice_NoProfiles(t *testing.T) {
	reg := NewRegistry()
	_, err := MatchDevice(context.Background(), "127.0.0.1", "u", "p", reg)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}

func TestMatchDevice_DisabledSkipped(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(v1.DeviceProfile{
		Spec: v1.DeviceProfileSpec{
			ProfileID:       "disabled",
			Enabled:         false,
			UpdateActionURI: "/redfish/v1/UpdateService/Actions/SimpleUpdate",
			Verification: v1.VerificationSpec{
				Path: "/redfish/v1/Managers/1", Field: "Model", Pattern: "^x",
			},
		},
	})

	_, err := MatchDevice(context.Background(), "127.0.0.1", "u", "p", reg)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch when only disabled profiles exist, got %v", err)
	}
}

// newRedfishTestServer returns a TLS test server that serves the given
// path->field map as JSON, and points httpClient at it.
func newRedfishTestServer(t *testing.T, responses map[string]map[string]interface{}) (host string, restore func()) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))

	prev := httpClient
	httpClient = srv.Client()

	host = strings.TrimPrefix(srv.URL, "https://")
	return host, func() {
		httpClient = prev
		srv.Close()
	}
}

func TestMatchDevice_Matches(t *testing.T) {
	host, restore := newRedfishTestServer(t, map[string]map[string]interface{}{
		"/redfish/v1/Managers/1": {"Model": "iLO 6"},
	})
	defer restore()

	reg := NewRegistry()
	reg.Upsert(v1.DeviceProfile{
		Spec: v1.DeviceProfileSpec{
			ProfileID:       "ilo",
			Enabled:         true,
			UpdateActionURI: "/redfish/v1/UpdateService/Actions/SimpleUpdate",
			Verification: v1.VerificationSpec{
				Path: "/redfish/v1/Managers/1", Field: "Model", Pattern: "^iLO",
			},
		},
	})

	got, err := MatchDevice(context.Background(), host, "u", "p", reg)
	if err != nil {
		t.Fatalf("MatchDevice: unexpected error: %v", err)
	}
	if got.Spec.ProfileID != "ilo" {
		t.Fatalf("matched profile = %q, want ilo", got.Spec.ProfileID)
	}
}

func TestReadDeviceIdentity(t *testing.T) {
	host, restore := newRedfishTestServer(t, map[string]map[string]interface{}{
		"/redfish/v1/Chassis/1": {"Manufacturer": "HPE", "Model": "ProLiant DL360"},
	})
	defer restore()

	p := v1.DeviceProfile{
		Spec: v1.DeviceProfileSpec{
			ManufacturerPath:  "/redfish/v1/Chassis/1",
			ManufacturerField: "Manufacturer",
			ModelPath:         "/redfish/v1/Chassis/1",
			ModelField:        "Model",
		},
	}

	mfr, model, err := ReadDeviceIdentity(context.Background(), host, "u", "p", p)
	if err != nil {
		t.Fatalf("ReadDeviceIdentity: unexpected error: %v", err)
	}
	if mfr != "HPE" {
		t.Errorf("manufacturer = %q, want HPE", mfr)
	}
	if model != "ProLiant DL360" {
		t.Errorf("model = %q, want ProLiant DL360", model)
	}
}

func TestExtractField_Nested(t *testing.T) {
	data := map[string]interface{}{
		"Status": map[string]interface{}{"Health": "OK"},
	}
	got, err := extractField(data, "Status.Health")
	if err != nil {
		t.Fatalf("extractField: unexpected error: %v", err)
	}
	if got != "OK" {
		t.Fatalf("got %q, want OK", got)
	}

	if _, err := extractField(data, "Status.Missing"); err == nil {
		t.Fatal("expected error for missing nested field")
	}
}
