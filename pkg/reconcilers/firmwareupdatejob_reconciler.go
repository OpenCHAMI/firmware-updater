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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/internal/smd"
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

	// Branch on BMC selector. Exactly one of GroupRef / TargetAddress is set
	// (enforced by Validate). Group mode fans out to the resolved member BMCs.
	if strings.TrimSpace(res.Spec.GroupRef) != "" {
		return r.reconcileGroup(ctx, res, proxyURI)
	}

	// Single-BMC path (existing behavior): discover action + targets and dispatch.
	taskID, err := r.dispatchToBMC(ctx, res, res.Spec.TargetAddress, res.Spec.Targets, proxyURI)
	if err != nil {
		res.Status.JobState = "Failed"
		res.Status.ErrorDetail = err.Error()
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			if isTerminalError(err) {
				return fmt.Errorf("set terminal failure after single-BMC dispatch error: %w", updateErr)
			}
			return fmt.Errorf("persist exhausted single-BMC transient error as failed: %w", updateErr)
		}
		return nil
	}

	res.Status.JobState = "InProgress"
	res.Status.TaskID = taskID
	res.Status.MemberCount = 1
	res.Status.CompletedCount = 1
	res.Status.ErrorDetail = ""

	return nil
}

// dispatchToBMC runs UpdateService action discovery, optional component-based
// target discovery, and the Redfish SimpleUpdate dispatch for a single BMC,
// returning the resulting task ID. Returned errors preserve terminal/transient
// classification (via *firmwareproxy.HTTPStatusError) for the caller.
func (r *FirmwareUpdateJobReconciler) dispatchToBMC(ctx context.Context, res *v1.FirmwareUpdateJob, bmcAddress string, targets []string, proxyURI string) (string, error) {
	actionURI, err := discoverUpdateServiceActionWithBackoff(ctx, bmcAddress, res.Spec.Username, res.Spec.Password)
	if err != nil {
		return "", fmt.Errorf("auto-discovery of UpdateService failed: %w", err)
	}
	r.Logger.Debugf("FirmwareUpdateJob %s discovered UpdateService action URI %s for BMC %s", res.GetUID(), actionURI, bmcAddress)

	// If Component is specified and Targets is empty, discover targets from
	// FirmwareInventory for this specific BMC.
	if res.Spec.Component != "" && len(targets) == 0 {
		discovered, err := discoverTargetsFromInventoryWithBackoff(ctx, bmcAddress, res.Spec.Username, res.Spec.Password, res.Spec.Component)
		if err != nil {
			return "", err
		}
		targets = discovered
		r.Logger.Debugf("FirmwareUpdateJob %s discovered targets for component %q on BMC %s: %v", res.GetUID(), res.Spec.Component, bmcAddress, targets)
	}

	taskID, err := dispatchRedfishWithBackoff(ctx, res, bmcAddress, targets, proxyURI, actionURI)
	if err != nil {
		return "", err
	}
	return taskID, nil
}

// reconcileGroup resolves an SMD group into member BMCs and fans out the
// per-BMC dispatch with bounded parallelism. The OCI payload has already been
// resolved once by the caller and is reused for every member via proxyURI.
func (r *FirmwareUpdateJobReconciler) reconcileGroup(ctx context.Context, res *v1.FirmwareUpdateJob, proxyURI string) error {
	resolver := smd.NewClientFromEnv()

	resolution, err := resolveGroupTargetsWithBackoff(ctx, resolver, res.Spec.GroupRef)
	if err != nil {
		detail := fmt.Sprintf("group %q resolution failed: %v", res.Spec.GroupRef, err)
		var httpErr *firmwareproxy.HTTPStatusError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			detail = fmt.Sprintf("group %q not found", res.Spec.GroupRef)
		}
		res.Status.JobState = "Failed"
		res.Status.ErrorDetail = detail
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("set failure after group resolution error: %w", updateErr)
		}
		return nil
	}

	res.Status.ResolutionDetail = resolution.Detail()
	res.Status.MemberCount = len(resolution.Targets)

	// Strict membership: unless AllowPartialTargets, any unresolvable member
	// fails the job.
	if len(resolution.Unresolvable) > 0 && !res.Spec.AllowPartialTargets {
		res.Status.JobState = "Failed"
		res.Status.FailedMembers = resolution.Unresolvable
		res.Status.ErrorDetail = fmt.Sprintf("group %q has %d unresolvable member(s) and allowPartialTargets is false", res.Spec.GroupRef, len(resolution.Unresolvable))
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("set failure after unresolvable members: %w", updateErr)
		}
		return nil
	}

	if len(resolution.Targets) == 0 {
		res.Status.JobState = "Failed"
		res.Status.ErrorDetail = fmt.Sprintf("group %q returned no resolvable members", res.Spec.GroupRef)
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("set failure after empty group resolution: %w", updateErr)
		}
		return nil
	}

	if len(resolution.Unresolvable) > 0 {
		r.Logger.Warnf("FirmwareUpdateJob %s proceeding with %d resolvable member(s); %d unresolvable: %v",
			res.GetUID(), len(resolution.Targets), len(resolution.Unresolvable), resolution.Unresolvable)
	}

	maxParallel := res.Spec.MaxParallel
	if maxParallel < 1 {
		maxParallel = 1
	}

	result := fanOutDispatch(ctx, resolution.Targets, maxParallel, func(ctx context.Context, m memberTarget) error {
		taskID, derr := r.dispatchToBMC(ctx, res, m.BMCFQDN, res.Spec.Targets, proxyURI)
		if derr != nil {
			r.Logger.Errorf("FirmwareUpdateJob %s member %s (%s) dispatch failed: %v", res.GetUID(), m.Xname, m.BMCFQDN, derr)
			return derr
		}
		r.Logger.Debugf("FirmwareUpdateJob %s member %s (%s) dispatched task %s", res.GetUID(), m.Xname, m.BMCFQDN, taskID)
		return nil
	})

	res.Status.CompletedCount = result.Completed
	res.Status.FailedMembers = result.Failed

	// All-members-must-succeed aggregation rule.
	if len(result.Failed) > 0 {
		res.Status.JobState = "Failed"
		res.Status.ErrorDetail = fmt.Sprintf("%d of %d member BMC(s) failed to dispatch: %v", len(result.Failed), len(resolution.Targets), result.FirstErr)
		if updateErr := r.UpdateStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("set failure after fan-out: %w", updateErr)
		}
		return nil
	}

	res.Status.JobState = "InProgress"
	res.Status.ErrorDetail = ""
	return nil
}

// resolveGroupTargetsWithBackoff wraps resolveGroupTargets with the same
// exponential-backoff retry used elsewhere, retrying only transient errors.
func resolveGroupTargetsWithBackoff(ctx context.Context, resolver smdResolver, groupRef string) (groupResolution, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		resolution, err := resolveGroupTargets(ctx, resolver, groupRef)
		if err == nil {
			return resolution, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return groupResolution{}, waitErr
		}
		backoff *= 2
	}

	return groupResolution{}, lastErr
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

func resolvePayloadFromDiscoveryWithBackoff(ctx context.Context, repository, hardwareModel, versionTarget string) (firmwareproxy.DiscoveryResult, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		resolved, err := firmwareproxy.ResolvePayloadFromDiscovery(ctx, repository, hardwareModel, versionTarget)
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

func dispatchRedfishWithBackoff(ctx context.Context, res *v1.FirmwareUpdateJob, bmcAddress string, targets []string, proxyURI, actionURI string) (string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		taskID, err := dispatchRedfishOnce(ctx, res, bmcAddress, targets, proxyURI, actionURI)
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

func dispatchRedfishOnce(ctx context.Context, res *v1.FirmwareUpdateJob, bmcAddress string, targets []string, proxyURI, actionURI string) (string, error) {
	payload := map[string]interface{}{
		"ImageURI":         proxyURI,
		"Targets":          targets,
		"TransferProtocol": "HTTP",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal Redfish SimpleUpdate body: %w", err)
	}

	// Construct the full endpoint URL if actionURI is a relative path
	endpoint := actionURI
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = fmt.Sprintf("https://%s%s", strings.TrimSpace(bmcAddress), actionURI)
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
	var statusErr *firmwareproxy.HTTPStatusError
	if !errors.As(err, &statusErr) {
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
