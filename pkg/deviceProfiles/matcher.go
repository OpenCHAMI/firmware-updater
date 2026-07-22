// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package deviceProfiles

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
)

// ErrNoMatch is returned by MatchDevice when no enabled profile matches.
var ErrNoMatch = errors.New("no device profile matched the target device")

// httpClient is a shared client that tolerates the self-signed certificates
// commonly presented by BMCs.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 - BMCs use self-signed certs
	},
}

// MatchDevice probes the target BMC and returns the first enabled profile whose
// Verification rule matches. It returns ErrNoMatch if none match.
func MatchDevice(ctx context.Context, targetAddress, username, password string, reg *Registry) (v1.DeviceProfile, error) {
	for _, p := range reg.List() {
		if !p.Spec.Enabled {
			continue
		}
		matched, err := verifyProfile(ctx, targetAddress, username, password, p)
		if err != nil {
			// Probe failed for this profile; try the next one.
			continue
		}
		if matched {
			return p, nil
		}
	}
	return v1.DeviceProfile{}, ErrNoMatch
}

// verifyProfile probes the device and reports whether it matches the profile's
// Verification rule.
func verifyProfile(ctx context.Context, targetAddress, username, password string, p v1.DeviceProfile) (bool, error) {
	if p.Spec.Verification.Path == "" || p.Spec.Verification.Field == "" {
		return false, fmt.Errorf("profile %q has incomplete verification spec", p.Spec.ProfileID)
	}

	value, err := readRedfieldField(ctx, targetAddress, username, password, p.Spec.Verification.Path, p.Spec.Verification.Field)
	if err != nil {
		return false, err
	}

	if p.Spec.Verification.Pattern == "" {
		return true, nil
	}

	rx, err := regexp.Compile(p.Spec.Verification.Pattern)
	if err != nil {
		return false, err
	}
	return rx.MatchString(value), nil
}

// ReadDeviceIdentity reads the manufacturer and model values using the paths and
// fields configured in the profile's Spec.
func ReadDeviceIdentity(ctx context.Context, targetAddress, username, password string, p v1.DeviceProfile) (manufacturer, model string, err error) {
	manufacturer, err = readRedfieldField(ctx, targetAddress, username, password, p.Spec.ManufacturerPath, p.Spec.ManufacturerField)
	if err != nil {
		return "", "", fmt.Errorf("read manufacturer: %w", err)
	}
	model, err = readRedfieldField(ctx, targetAddress, username, password, p.Spec.ModelPath, p.Spec.ModelField)
	if err != nil {
		return "", "", fmt.Errorf("read model: %w", err)
	}
	return manufacturer, model, nil
}

// readRedfieldField GETs a Redfish path and extracts the named field. The field
// name supports dot notation for nested objects (e.g. "Status.Health").
func readRedfieldField(ctx context.Context, targetAddress, username, password, path, field string) (string, error) {
	url := fmt.Sprintf("https://%s%s", targetAddress, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d reading %s", resp.StatusCode, path)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("parse JSON from %s: %w", path, err)
	}

	return extractField(data, field)
}

// extractField walks a decoded JSON object following a dot-separated field path
// and returns the terminal value formatted as a string.
func extractField(data map[string]interface{}, field string) (string, error) {
	var current interface{} = data
	for _, part := range strings.Split(field, ".") {
		obj, ok := current.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("cannot traverse to field %q", field)
		}
		current, ok = obj[part]
		if !ok {
			return "", fmt.Errorf("field %q not found", field)
		}
	}

	switch v := current.(type) {
	case string:
		return v, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// BuildUpdatePayload resolves %placeholder% tokens in Spec.UpdatePayloadTemplate
// using the supplied substitutions and returns the resulting JSON bytes.
// Recognized placeholders include %imageURI%, %target%, %component%, and
// %applyTime%; any key present in subs is substituted.
func BuildUpdatePayload(p v1.DeviceProfile, subs map[string]string) ([]byte, error) {
	rendered := p.Spec.UpdatePayloadTemplate
	for key, value := range subs {
		rendered = strings.ReplaceAll(rendered, "%"+key+"%", value)
	}

	var check interface{}
	if err := json.Unmarshal([]byte(rendered), &check); err != nil {
		return nil, fmt.Errorf("payload after substitution is not valid JSON: %w", err)
	}
	return []byte(rendered), nil
}
