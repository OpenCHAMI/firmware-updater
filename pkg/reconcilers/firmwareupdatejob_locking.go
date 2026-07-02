// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package reconcilers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

const (
	smdBaseURLEnvVar   = "FIRMWARE_UPDATER_SMD_BASE_URL"
	smdTokenEnvVar     = "FIRMWARE_UPDATER_SMD_TOKEN"
	defaultSMDBaseURL  = "http://localhost:27779"
	smdLockStatusRoute = "/hsm/v2/locks/status"
)

type smdLockStatusFilter struct {
	ComponentIDs []string `json:"ComponentIDs"`
}

type smdLockStatusResponse struct {
	Components []smdLockComponent `json:"Components"`
	NotFound   []string           `json:"NotFound"`
}

type smdLockComponent struct {
	ID             string `json:"ID"`
	Locked         bool   `json:"Locked"`
	Reserved       bool   `json:"Reserved"`
	CreationTime   string `json:"CreationTime"`
	ExpirationTime string `json:"ExpirationTime"`
}

func evaluateTargetLocksWithBackoff(ctx context.Context, componentIDs []string) ([]string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		conflicts, err := evaluateTargetLocks(ctx, componentIDs)
		if err == nil {
			return conflicts, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return nil, waitErr
		}
		backoff *= 2
	}

	return nil, lastErr
}

func evaluateTargetLocks(ctx context.Context, componentIDs []string) ([]string, error) {
	status, err := querySMDLockStatus(ctx, componentIDs)
	if err != nil {
		return nil, err
	}

	return lockConflictsFromStatus(status), nil
}

func querySMDLockStatus(ctx context.Context, componentIDs []string) (smdLockStatusResponse, error) {
	targets := compactStrings(componentIDs)
	if len(targets) == 0 {
		return smdLockStatusResponse{}, nil
	}

	requestBody, err := json.Marshal(smdLockStatusFilter{ComponentIDs: targets})
	if err != nil {
		return smdLockStatusResponse{}, fmt.Errorf("marshal SMD lock status request: %w", err)
	}

	endpoint := strings.TrimRight(getSMDBaseURL(), "/") + smdLockStatusRoute
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return smdLockStatusResponse{}, fmt.Errorf("build SMD lock status request: %w", err)
	}
	if token := strings.TrimSpace(os.Getenv(smdTokenEnvVar)); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		if isLikelyTransientNetworkError(err) {
			return smdLockStatusResponse{}, &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: err.Error()}
		}
		return smdLockStatusResponse{}, fmt.Errorf("SMD lock status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		return smdLockStatusResponse{}, &firmwareproxy.HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("SMD lock status returned %s", resp.Status)}
	}
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode >= 500 {
		return smdLockStatusResponse{}, &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: fmt.Sprintf("SMD lock status returned %s", resp.Status)}
	}

	var status smdLockStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return smdLockStatusResponse{}, fmt.Errorf("parse SMD lock status response: %w", err)
	}

	return status, nil
}

func getSMDBaseURL() string {
	if configured := strings.TrimSpace(os.Getenv(smdBaseURLEnvVar)); configured != "" {
		return configured
	}
	return defaultSMDBaseURL
}

func lockConflictsFromStatus(status smdLockStatusResponse) []string {
	conflicts := make([]string, 0, len(status.Components)+len(status.NotFound))
	for _, component := range status.Components {
		if component.Locked || component.Reserved {
			conflicts = append(conflicts, fmt.Sprintf(
				"component=%s locked=%t reserved=%t creationTime=%s expirationTime=%s",
				component.ID,
				component.Locked,
				component.Reserved,
				emptyFallback(component.CreationTime, "n/a"),
				emptyFallback(component.ExpirationTime, "n/a"),
			))
		}
	}

	for _, id := range compactStrings(status.NotFound) {
		conflicts = append(conflicts, fmt.Sprintf("component=%s notFound=true", id))
	}

	sort.Strings(conflicts)
	return conflicts
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	sort.Strings(result)
	return result
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
