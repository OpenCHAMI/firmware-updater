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
	"sync"
	"time"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

var (
	firmwareResolverMu sync.RWMutex
	firmwareResolver   = firmwareproxy.NewResolver("", "", "")
)

func SetFirmwareProxyResolver(resolver *firmwareproxy.Resolver) {
	firmwareResolverMu.Lock()
	defer firmwareResolverMu.Unlock()

	if resolver == nil {
		firmwareResolver = firmwareproxy.NewResolver("", "", "")
		return
	}

	firmwareResolver = resolver
}

func getFirmwareProxyResolver() *firmwareproxy.Resolver {
	firmwareResolverMu.RLock()
	defer firmwareResolverMu.RUnlock()
	return firmwareResolver
}

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

	res.Status.JobState = "Resolving"
	res.Status.ErrorDetail = ""
	if err := r.UpdateStatus(ctx, res); err != nil {
		return fmt.Errorf("update status to Resolving: %w", err)
	}

	var (
		payloadDigest   string
		resolvedVersion string
		resolvedRef     string
		err             error
	)

	if res.Spec.OCIReference != nil {
		payloadDigest, err = resolvePayloadWithBackoff(ctx, *res.Spec.OCIReference)
		resolvedRef = *res.Spec.OCIReference
	} else if res.Spec.Discovery != nil {
		resolved, resolveErr := resolvePayloadFromDiscoveryWithBackoff(
			ctx,
			res.Spec.Discovery.Repository,
			res.Spec.Discovery.HardwareModel,
			res.Spec.Discovery.Version,
		)
		err = resolveErr
		if resolveErr == nil {
			payloadDigest = resolved.Digest
			resolvedVersion = resolved.Version
			resolvedRef = resolved.OCIReference
		}
	} else {
		err = &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "missing both spec.ociReference and spec.discovery"}
	}

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

	res.Status.ResolvedDigest = payloadDigest
	res.Status.ResolvedVersion = resolvedVersion
	r.Logger.Debugf("FirmwareUpdateJob %s resolved payload digest %q from %q", res.GetUID(), payloadDigest, resolvedRef)

	proxyURI := fmt.Sprintf("http://%s/firmware-proxy/layer/%s", net.JoinHostPort(res.Spec.ServerProxyAddress, "8090"), payloadDigest)

	// Discover the UpdateService action URI
	actionURI, err := discoverUpdateServiceActionWithBackoff(ctx, res.Spec.TargetAddress, res.Spec.Username, res.Spec.Password)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = fmt.Sprintf("auto-discovery of UpdateService failed: %v", err)
			if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("set terminal failure after UpdateService discovery error: %w", updateErr)
			}
			return nil
		}

		res.Status.ErrorDetail = fmt.Sprintf("auto-discovery of UpdateService failed: %v", err)
		res.Status.JobState = "Failed"
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("persist exhausted UpdateService discovery transient error as failed: %w", updateErr)
		}
		return nil
	}

	r.Logger.Debugf("FirmwareUpdateJob %s discovered UpdateService action URI: %s", res.GetUID(), actionURI)

	// If Component is specified and Targets is empty, discover targets from FirmwareInventory
	if res.Spec.Component != "" && len(res.Spec.Targets) == 0 {
		targets, err := discoverTargetsFromInventoryWithBackoff(ctx, res.Spec.TargetAddress, res.Spec.Username, res.Spec.Password, res.Spec.Component)
		if err != nil {
			if isTerminalError(err) {
				res.Status.JobState = "Failed"
				res.Status.ErrorDetail = err.Error()
				if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
					return fmt.Errorf("set terminal failure after FirmwareInventory discovery error: %w", updateErr)
				}
				return nil
			}

			res.Status.ErrorDetail = err.Error()
			res.Status.JobState = "Failed"
			if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("persist exhausted FirmwareInventory discovery transient error as failed: %w", updateErr)
			}
			return nil
		}

		res.Spec.Targets = targets
		r.Logger.Debugf("FirmwareUpdateJob %s discovered targets for component %q: %v", res.GetUID(), res.Spec.Component, targets)
	}

	taskID, err := dispatchRedfishWithBackoff(ctx, res, proxyURI, actionURI)
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
	resolver := getFirmwareProxyResolver()

	for attempt := 1; attempt <= 4; attempt++ {
		payloadDigest, err := resolver.ResolvePayload(ctx, ociReference)
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

func resolvePayloadFromDiscoveryWithBackoff(ctx context.Context, repository, hardwareModel, versionTarget string) (firmwareproxy.DiscoveryResult, error) {
	var lastErr error
	backoff := time.Second
	resolver := getFirmwareProxyResolver()

	for attempt := 1; attempt <= 4; attempt++ {
		resolved, err := resolver.ResolvePayloadFromDiscovery(ctx, repository, hardwareModel, versionTarget)
		if err == nil {
			return resolved, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return firmwareproxy.DiscoveryResult{}, waitErr
		}
		backoff *= 2
	}

	return firmwareproxy.DiscoveryResult{}, lastErr
}

func discoverUpdateServiceActionWithBackoff(ctx context.Context, targetAddress, username, password string) (string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		actionURI, err := discoverUpdateServiceAction(ctx, targetAddress, username, password)
		if err == nil {
			return actionURI, nil
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

func discoverTargetsFromInventoryWithBackoff(ctx context.Context, targetAddress, username, password, component string) ([]string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		targets, err := discoverTargetsFromInventory(ctx, targetAddress, username, password, component)
		if err == nil {
			return targets, nil
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

func dispatchRedfishWithBackoff(ctx context.Context, res *v1.FirmwareUpdateJob, proxyURI, actionURI string) (string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		taskID, err := dispatchRedfishOnce(ctx, res, proxyURI, actionURI)
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

func dispatchRedfishOnce(ctx context.Context, res *v1.FirmwareUpdateJob, proxyURI, actionURI string) (string, error) {
	payload := map[string]interface{}{
		"ImageURI":         proxyURI,
		"Targets":          res.Spec.Targets,
		"TransferProtocol": "HTTP",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal Redfish SimpleUpdate body: %w", err)
	}

	// Construct the full endpoint URL if actionURI is a relative path
	endpoint := actionURI
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = fmt.Sprintf("https://%s%s", strings.TrimSpace(res.Spec.TargetAddress), actionURI)
	}

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

// discoverUpdateServiceAction queries the UpdateService endpoint and returns the SimpleUpdate action URI
func discoverUpdateServiceAction(ctx context.Context, targetAddress, username, password string) (string, error) {
	endpoint := fmt.Sprintf("https://%s/redfish/v1/UpdateService", strings.TrimSpace(targetAddress))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build UpdateService GET request: %w", err)
	}
	req.SetBasicAuth(username, password)

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
		return "", fmt.Errorf("UpdateService GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		return "", &firmwareproxy.HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("UpdateService returned %s", resp.Status)}
	}
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode >= 500 {
		return "", &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: fmt.Sprintf("UpdateService returned %s", resp.Status)}
	}

	var updateService map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&updateService); err != nil {
		return "", fmt.Errorf("parse UpdateService response: %w", err)
	}

	// Look for Actions object
	actions, ok := updateService["Actions"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("auto-discovery failed: no Actions object in UpdateService response")
	}

	// Try to find SimpleUpdate action with either key format
	var actionTarget string
	if simpleUpdate, ok := actions["#UpdateService.SimpleUpdate"].(map[string]interface{}); ok {
		if target, ok := simpleUpdate["target"].(string); ok {
			actionTarget = target
		}
	} else if simpleUpdate, ok := actions["#SimpleUpdate"].(map[string]interface{}); ok {
		if target, ok := simpleUpdate["target"].(string); ok {
			actionTarget = target
		}
	}

	if actionTarget == "" {
		return "", fmt.Errorf("auto-discovery failed: no SimpleUpdate action found in UpdateService")
	}

	return actionTarget, nil
}

// discoverTargetsFromInventory queries FirmwareInventory and returns targets matching the component
func discoverTargetsFromInventory(ctx context.Context, targetAddress, username, password, component string) ([]string, error) {
	endpoint := fmt.Sprintf("https://%s/redfish/v1/UpdateService/FirmwareInventory", strings.TrimSpace(targetAddress))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build FirmwareInventory GET request: %w", err)
	}
	req.SetBasicAuth(username, password)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		if isLikelyTransientNetworkError(err) {
			return nil, &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: err.Error()}
		}
		return nil, fmt.Errorf("FirmwareInventory GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		return nil, &firmwareproxy.HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("FirmwareInventory returned %s", resp.Status)}
	}
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode >= 500 {
		return nil, &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: fmt.Sprintf("FirmwareInventory returned %s", resp.Status)}
	}

	var inventory map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&inventory); err != nil {
		return nil, fmt.Errorf("parse FirmwareInventory response: %w", err)
	}

	members, ok := inventory["Members"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("auto-discovery failed: no Members array in FirmwareInventory response")
	}

	var targets []string
	componentLower := strings.ToLower(component)

	for _, member := range members {
		memberMap, ok := member.(map[string]interface{})
		if !ok {
			continue
		}

		// Get the @odata.id for this member
		memberID, ok := memberMap["@odata.id"].(string)
		if !ok || memberID == "" {
			continue
		}

		// Fetch the member details
		memberReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s%s", strings.TrimSpace(targetAddress), memberID), nil)
		if err != nil {
			continue
		}
		memberReq.SetBasicAuth(username, password)

		memberResp, err := client.Do(memberReq)
		if err != nil || memberResp.StatusCode != http.StatusOK {
			if memberResp != nil {
				memberResp.Body.Close()
			}
			continue
		}

		var memberDetail map[string]interface{}
		if err := json.NewDecoder(memberResp.Body).Decode(&memberDetail); err != nil {
			memberResp.Body.Close()
			continue
		}
		memberResp.Body.Close()

		// Check Id, Name, and Description fields for component match
		if id, ok := memberDetail["Id"].(string); ok && strings.Contains(strings.ToLower(id), componentLower) {
			targets = append(targets, memberID)
			continue
		}
		if name, ok := memberDetail["Name"].(string); ok && strings.Contains(strings.ToLower(name), componentLower) {
			targets = append(targets, memberID)
			continue
		}
		if description, ok := memberDetail["Description"].(string); ok && strings.Contains(strings.ToLower(description), componentLower) {
			targets = append(targets, memberID)
			continue
		}
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("auto-discovery failed: component %q not found in FirmwareInventory", component)
	}

	return targets, nil
}
