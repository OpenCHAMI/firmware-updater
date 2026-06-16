// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareUpdateJob.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

// reconcileFirmwareUpdateJob contains custom reconciliation logic.
//
// This method is called by the generated Reconcile() orchestration method.
// Implement FirmwareUpdateJob-specific reconciliation logic here.
//
// Guidelines:
//  1. Keep this method idempotent (safe to call multiple times)
//  2. Update Status fields to reflect observed state
//  3. Emit events for significant state changes using r.EmitEvent()
//  4. Use r.Logger for debugging (Infof, Warnf, Errorf, Debugf)
//  5. Return errors for transient failures (will retry with backoff)
//  6. Access storage via r.Client (Get, List, Update, Create, Delete)
//
// Example implementation patterns:
//
// For hardware resources (BMC, Node):
//   - Connect to hardware endpoint
//   - Query current state
//   - Update Status.Connected, Status.Version, Status.Health
//   - Emit events when state changes
//
// For hierarchical resources (Rack, Chassis):
//   - Create/reconcile child resources
//   - Update Status with child counts and references
//   - Emit events when topology changes
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - res: The FirmwareUpdateJob resource to reconcile
//
// Returns:
//   - error: If reconciliation failed (will trigger retry with backoff)
func (r *FirmwareUpdateJobReconciler) reconcileFirmwareUpdateJob(ctx context.Context, res *v1.FirmwareUpdateJob) error {
	if res.Status.JobState == "" {
		res.Status.JobState = "Pending"
	}

	if res.Status.JobState == "InProgress" || res.Status.JobState == "Completed" || res.Status.JobState == "Failed" {
		r.Logger.Infof("FirmwareUpdateJob %s already terminal or active in state %q; skipping", res.GetUID(), res.Status.JobState)
		return nil
	}

	// Pre-flight: resolve and validate UpdateURI
	updateURI := strings.TrimSpace(res.Spec.UpdateURI)
	if updateURI == "" {
		updateURI = "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate"
	} else if !strings.HasPrefix(updateURI, "/redfish/v1/") {
		res.Status.JobState = "Failed"
		res.Status.ErrorDetail = "invalid UpdateURI: must begin with /redfish/v1/"
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("set terminal failure on invalid UpdateURI: %w", updateErr)
		}
		return nil
	}

	res.Status.JobState = "Resolving"
	res.Status.ErrorDetail = ""
	if err := r.UpdateStatus(ctx, res); err != nil {
		return fmt.Errorf("update status to Resolving: %w", err)
	}

	payloadDigest, err := resolvePayloadWithBackoff(ctx, res.Spec.OCIReference)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = err.Error()
			if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("set terminal failure after ORAS resolve error: %w", updateErr)
			}
			return nil
		}

		res.Status.ErrorDetail = err.Error()
		res.Status.JobState = "Failed"
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("persist exhausted ORAS transient error as failed: %w", updateErr)
		}
		return nil
	}

	proxyURI := fmt.Sprintf("http://%s/firmware-proxy/layer/%s", net.JoinHostPort(res.Spec.ServerProxyAddress, "8090"), payloadDigest)

	taskID, err := dispatchRedfishWithBackoff(ctx, res, proxyURI, updateURI)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = err.Error()
			if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("set terminal failure after Redfish dispatch error: %w", updateErr)
			}
			return nil
		}

		res.Status.ErrorDetail = err.Error()
		res.Status.JobState = "Failed"
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("persist exhausted Redfish transient error as failed: %w", updateErr)
		}
		return nil
	}

	res.Status.JobState = "InProgress"
	res.Status.TaskID = taskID
	res.Status.ErrorDetail = ""

	return nil
}

func resolvePayloadWithBackoff(ctx context.Context, ociReference string) (string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		payloadDigest, err := firmwareproxy.ResolvePayload(ctx, ociReference)
		if err == nil {
			return payloadDigest, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return "", waitErr
		}
		backoff *= 2
	}

	return "", lastErr
}

func dispatchRedfishWithBackoff(ctx context.Context, res *v1.FirmwareUpdateJob, proxyURI string, updateURI string) (string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		taskID, err := dispatchRedfishOnce(ctx, res, proxyURI, updateURI)
		if err == nil {
			return taskID, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return "", waitErr
		}
		backoff *= 2
	}

	return "", lastErr
}

func dispatchRedfishOnce(ctx context.Context, res *v1.FirmwareUpdateJob, proxyURI string, updateURI string) (string, error) {
	payload := map[string]interface{}{
		"ImageURI":         proxyURI,
		"Targets":          res.Spec.Targets,
		"TransferProtocol": "HTTP",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal Redfish SimpleUpdate body: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s%s", strings.TrimSpace(res.Spec.TargetAddress), updateURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("build Redfish SimpleUpdate request: %w", err)
	}
	req.SetBasicAuth(res.Spec.Username, res.Spec.Password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		if isLikelyTransientNetworkError(err) {
			return "", &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: err.Error()}
		}
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		return "", &firmwareproxy.HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("Redfish returned %s", resp.Status)}
	}
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode >= 500 {
		return "", &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: fmt.Sprintf("Redfish returned %s", resp.Status)}
	}

	taskID := strings.TrimSpace(resp.Header.Get("Location"))
	if taskID == "" {
		var bodyObj map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&bodyObj); err == nil {
			if v, ok := bodyObj["@odata.id"].(string); ok {
				taskID = v
			} else if v, ok := bodyObj["TaskID"].(string); ok {
				taskID = v
			}
		}
	}

	return taskID, nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func isTerminalError(err error) bool {
	statusErr, ok := err.(*firmwareproxy.HTTPStatusError)
	if !ok {
		return false
	}

	return statusErr.StatusCode >= 400 && statusErr.StatusCode < 500
}

func isLikelyTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}

	if ue, ok := err.(*url.Error); ok {
		err = ue.Err
	}

	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout() || netErr.Temporary()
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "no route to host")
}
