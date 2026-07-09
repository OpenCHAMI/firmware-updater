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

	"github.com/openchami/fabrica/pkg/events"
	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/internal/secretsruntime"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

type bmcCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
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

	if res.Status.JobState == "Completed" || res.Status.JobState == "Failed" {
		r.Logger.Infof("FirmwareUpdateJob %s already terminal or active in state %q; skipping", res.GetUID(), res.Status.JobState)
		return nil
	}

	if res.Status.JobState == "InProgress" {
		return r.observeInProgressFirmwareUpdateJob(ctx, res)
	}

	res.Status.JobState = "Resolving"
	res.Status.ErrorDetail = ""
	if err := r.updateJobStatus(ctx, res); err != nil {
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
			if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("set terminal failure after ORAS resolve error: %w", updateErr)
			}
			return nil
		}

		res.Status.ErrorDetail = err.Error()
		res.Status.JobState = "Failed"
		if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("persist exhausted ORAS transient error as failed: %w", updateErr)
		}
		return nil
	}

	res.Status.ResolvedDigest = payloadDigest
	res.Status.ResolvedVersion = resolvedVersion
	r.Logger.Debugf("FirmwareUpdateJob %s resolved payload digest %q from %q", res.GetUID(), payloadDigest, resolvedRef)

	creds, err := loadBMCCredentials(res.Spec.SecretID)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = err.Error()
			if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("set terminal failure after credential load error: %w", updateErr)
			}
			return nil
		}

		res.Status.ErrorDetail = err.Error()
		res.Status.JobState = "Failed"
		if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("persist credential load error as failed: %w", updateErr)
		}
		return nil
	}

	proxyURI := fmt.Sprintf("http://%s/firmware-proxy/layer/%s", net.JoinHostPort(res.Spec.ServerProxyAddress, "8090"), payloadDigest)

	// Discover the UpdateService action URI
	actionURI, err := discoverUpdateServiceActionWithBackoff(ctx, res.Spec.TargetAddress, creds.Username, creds.Password)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = fmt.Sprintf("auto-discovery of UpdateService failed: %v", err)
			if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("set terminal failure after UpdateService discovery error: %w", updateErr)
			}
			return nil
		}

		res.Status.ErrorDetail = fmt.Sprintf("auto-discovery of UpdateService failed: %v", err)
		res.Status.JobState = "Failed"
		if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("persist exhausted UpdateService discovery transient error as failed: %w", updateErr)
		}
		return nil
	}

	r.Logger.Debugf("FirmwareUpdateJob %s discovered UpdateService action URI: %s", res.GetUID(), actionURI)

	// If Component is specified and Targets is empty, discover targets from FirmwareInventory
	if res.Spec.Component != "" && len(res.Spec.Targets) == 0 {
		targets, err := discoverTargetsFromInventoryWithBackoff(ctx, res.Spec.TargetAddress, creds.Username, creds.Password, res.Spec.Component)
		if err != nil {
			if isTerminalError(err) {
				res.Status.JobState = "Failed"
				res.Status.ErrorDetail = err.Error()
				if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
					return fmt.Errorf("set terminal failure after FirmwareInventory discovery error: %w", updateErr)
				}
				return nil
			}

			res.Status.ErrorDetail = err.Error()
			res.Status.JobState = "Failed"
			if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("persist exhausted FirmwareInventory discovery transient error as failed: %w", updateErr)
			}
			return nil
		}

		res.Spec.Targets = targets
		r.Logger.Debugf("FirmwareUpdateJob %s discovered targets for component %q: %v", res.GetUID(), res.Spec.Component, targets)
	}

	taskID, err := dispatchRedfishWithBackoff(ctx, res, creds, proxyURI, actionURI)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = err.Error()
			if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
				return fmt.Errorf("set terminal failure after Redfish dispatch error: %w", updateErr)
			}
			return nil
		}

		res.Status.ErrorDetail = err.Error()
		res.Status.JobState = "Failed"
		if updateErr := r.updateJobStatus(ctx, res); updateErr != nil {
			return fmt.Errorf("persist exhausted Redfish transient error as failed: %w", updateErr)
		}
		return nil
	}

	res.Status.JobState = "InProgress"
	res.Status.TaskID = taskID
	res.Status.ErrorDetail = ""

	return nil
}

func (r *FirmwareUpdateJobReconciler) updateJobStatus(ctx context.Context, res *v1.FirmwareUpdateJob) error {
	previousState, err := r.loadStoredJobState(ctx, res)
	if err != nil {
		return err
	}

	if err := r.UpdateStatus(ctx, res); err != nil {
		return err
	}

	if !jobReleasedTarget(previousState, res.Status.JobState) {
		return nil
	}

	if err := r.notifyOwningCampaignTargetReleased(ctx, res, previousState); err != nil {
		r.Logger.Warnf("Failed to notify owning FirmwareUpdateCampaign for child job %s: %v", res.GetUID(), err)
	}

	return nil
}

func (r *FirmwareUpdateJobReconciler) loadStoredJobState(ctx context.Context, res *v1.FirmwareUpdateJob) (string, error) {
	if r.Client == nil || strings.TrimSpace(res.GetUID()) == "" {
		return "", nil
	}

	current, err := r.Client.Get(ctx, "FirmwareUpdateJob", res.GetUID())
	if err != nil {
		return "", fmt.Errorf("load current FirmwareUpdateJob %q: %w", res.GetUID(), err)
	}

	job, ok := current.(*v1.FirmwareUpdateJob)
	if !ok {
		return "", fmt.Errorf("load current FirmwareUpdateJob %q: unexpected type %T", res.GetUID(), current)
	}

	return strings.TrimSpace(job.Status.JobState), nil
}

func (r *FirmwareUpdateJobReconciler) notifyOwningCampaignTargetReleased(ctx context.Context, res *v1.FirmwareUpdateJob, previousState string) error {
	campaignUID := strings.TrimSpace(res.Metadata.Annotations[v1.CampaignUIDAnnotation])
	if campaignUID == "" || r.Client == nil {
		return nil
	}

	current, err := r.Client.Get(ctx, "FirmwareUpdateCampaign", campaignUID)
	if err != nil {
		return fmt.Errorf("load owning FirmwareUpdateCampaign %q: %w", campaignUID, err)
	}

	campaign, ok := current.(*v1.FirmwareUpdateCampaign)
	if !ok {
		return fmt.Errorf("load owning FirmwareUpdateCampaign %q: unexpected type %T", campaignUID, current)
	}

	metadata := map[string]interface{}{
		"trigger":          "child-job-released-target",
		"childJobUID":      res.GetUID(),
		"previousJobState": strings.TrimSpace(previousState),
		"currentJobState":  strings.TrimSpace(res.Status.JobState),
	}

	return events.PublishResourceUpdated(ctx, "FirmwareUpdateCampaign", campaign.Metadata.UID, campaign.Metadata.Name, campaign, metadata)
}

func jobReleasedTarget(previousState, currentState string) bool {
	return jobStateIsActive(previousState) && !jobStateIsActive(currentState)
}

func jobStateIsActive(state string) bool {
	trimmed := strings.TrimSpace(state)
	return trimmed != v1.CampaignStateCompleted && trimmed != v1.CampaignStateFailed
}

func (r *FirmwareUpdateJobReconciler) observeInProgressFirmwareUpdateJob(ctx context.Context, res *v1.FirmwareUpdateJob) error {
	creds, err := loadBMCCredentials(res.Spec.SecretID)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = err.Error()
			return nil
		}

		return err
	}

	if taskID := strings.TrimSpace(res.Status.TaskID); taskID != "" {
		observation, err := pollRedfishTaskWithBackoff(ctx, res.Spec.TargetAddress, creds.Username, creds.Password, taskID)
		if err != nil {
			if isTerminalError(err) {
				res.Status.JobState = "Failed"
				res.Status.ErrorDetail = fmt.Sprintf("poll Redfish task failed: %v", err)
				return nil
			}

			return err
		}

		switch observation.State {
		case redfishTaskStateCompleted:
			res.Status.JobState = "Completed"
			res.Status.ErrorDetail = ""
			return nil
		case redfishTaskStateFailed:
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = observation.Detail
			if res.Status.ErrorDetail == "" {
				res.Status.ErrorDetail = "Redfish task reported failure"
			}
			return nil
		case redfishTaskStateRunning:
			return nil
		case redfishTaskStateMissing:
			// Some BMCs delete finished task resources quickly. Fall back to inventory verification.
		}
	}

	verification, err := verifyFirmwareTargetsUpdatedWithBackoff(ctx, res, creds)
	if err != nil {
		if isTerminalError(err) {
			res.Status.JobState = "Failed"
			res.Status.ErrorDetail = fmt.Sprintf("verify firmware inventory failed: %v", err)
			return nil
		}

		return err
	}

	if verification.Failed {
		res.Status.JobState = "Failed"
		res.Status.ErrorDetail = verification.Detail
		if res.Status.ErrorDetail == "" {
			res.Status.ErrorDetail = "Redfish inventory reported failure"
		}
		return nil
	}

	if verification.Updated {
		res.Status.JobState = "Completed"
		res.Status.ErrorDetail = ""
	}

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

func dispatchRedfishWithBackoff(ctx context.Context, res *v1.FirmwareUpdateJob, creds bmcCredentials, proxyURI, actionURI string) (string, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		taskID, err := dispatchRedfishOnce(ctx, res, creds, proxyURI, actionURI)
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

func dispatchRedfishOnce(ctx context.Context, res *v1.FirmwareUpdateJob, creds bmcCredentials, proxyURI, actionURI string) (string, error) {
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
	endpoint := resolveRedfishEndpoint(res.Spec.TargetAddress, actionURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("build Redfish SimpleUpdate request: %w", err)
	}
	req.SetBasicAuth(creds.Username, creds.Password)
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

type redfishTaskState string

const (
	redfishTaskStateRunning   redfishTaskState = "running"
	redfishTaskStateCompleted redfishTaskState = "completed"
	redfishTaskStateFailed    redfishTaskState = "failed"
	redfishTaskStateMissing   redfishTaskState = "missing"
)

type redfishTaskObservation struct {
	State  redfishTaskState
	Detail string
}

func pollRedfishTaskWithBackoff(ctx context.Context, targetAddress, username, password, taskID string) (redfishTaskObservation, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		observation, err := pollRedfishTaskOnce(ctx, targetAddress, username, password, taskID)
		if err == nil {
			return observation, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return redfishTaskObservation{}, waitErr
		}
		backoff *= 2
	}

	return redfishTaskObservation{}, lastErr
}

func pollRedfishTaskOnce(ctx context.Context, targetAddress, username, password, taskID string) (redfishTaskObservation, error) {
	body, statusCode, err := getRedfishJSON(ctx, targetAddress, username, password, taskID)
	if err != nil {
		statusErr, ok := err.(*firmwareproxy.HTTPStatusError)
		if ok && statusErr.StatusCode == http.StatusNotFound {
			return redfishTaskObservation{State: redfishTaskStateMissing}, nil
		}
		return redfishTaskObservation{}, err
	}

	if statusCode == http.StatusAccepted {
		return redfishTaskObservation{State: redfishTaskStateRunning}, nil
	}

	taskState := strings.ToLower(strings.TrimSpace(asString(body["TaskState"])))
	taskStatus := strings.ToLower(strings.TrimSpace(asString(body["TaskStatus"])))
	detail := redfishTaskDetail(body)

	switch taskState {
	case "completed":
		if taskStatus == "critical" || taskStatus == "warning" {
			return redfishTaskObservation{State: redfishTaskStateFailed, Detail: detail}, nil
		}
		return redfishTaskObservation{State: redfishTaskStateCompleted, Detail: detail}, nil
	case "exception", "killed", "cancelled", "canceled", "interrupted":
		return redfishTaskObservation{State: redfishTaskStateFailed, Detail: detail}, nil
	case "", "new", "pending", "starting", "running", "suspended", "stopping", "service", "canceling":
		if taskStatus == "critical" {
			return redfishTaskObservation{State: redfishTaskStateFailed, Detail: detail}, nil
		}
		return redfishTaskObservation{State: redfishTaskStateRunning, Detail: detail}, nil
	default:
		if taskStatus == "ok" {
			return redfishTaskObservation{State: redfishTaskStateCompleted, Detail: detail}, nil
		}
		if taskStatus == "critical" || taskStatus == "warning" {
			return redfishTaskObservation{State: redfishTaskStateFailed, Detail: detail}, nil
		}
		return redfishTaskObservation{State: redfishTaskStateRunning, Detail: detail}, nil
	}
}

type redfishInventoryVerification struct {
	Updated bool
	Failed  bool
	Detail  string
}

func verifyFirmwareTargetsUpdatedWithBackoff(ctx context.Context, res *v1.FirmwareUpdateJob, creds bmcCredentials) (redfishInventoryVerification, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		verification, err := verifyFirmwareTargetsUpdatedOnce(ctx, res, creds)
		if err == nil {
			return verification, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return redfishInventoryVerification{}, waitErr
		}
		backoff *= 2
	}

	return redfishInventoryVerification{}, lastErr
}

func verifyFirmwareTargetsUpdatedOnce(ctx context.Context, res *v1.FirmwareUpdateJob, creds bmcCredentials) (redfishInventoryVerification, error) {
	targets := append([]string(nil), res.Spec.Targets...)
	if len(targets) == 0 && strings.TrimSpace(res.Spec.Component) != "" {
		resolvedTargets, err := discoverTargetsFromInventory(ctx, res.Spec.TargetAddress, creds.Username, creds.Password, res.Spec.Component)
		if err != nil {
			return redfishInventoryVerification{}, err
		}
		targets = resolvedTargets
	}

	if len(targets) == 0 {
		return redfishInventoryVerification{}, nil
	}

	resolvedVersion := strings.ToLower(strings.TrimSpace(res.Status.ResolvedVersion))

	for _, target := range targets {
		body, _, err := getRedfishJSON(ctx, res.Spec.TargetAddress, creds.Username, creds.Password, target)
		if err != nil {
			return redfishInventoryVerification{}, err
		}

		// Unconditionally check for a failure state in the component inventory first
		if failed, detail := redfishInventoryFailure(body); failed {
			return redfishInventoryVerification{Failed: true, Detail: detail}, nil
		}

		// Only evaluate version completion if a target version is known
		if resolvedVersion != "" {
			installedVersion := strings.ToLower(strings.TrimSpace(asString(body["Version"])))
			if installedVersion == "" || !strings.Contains(installedVersion, resolvedVersion) {
				return redfishInventoryVerification{}, nil
			}
		} else {
			// If OCI reference was used, ResolvedVersion is empty. We can detect failures,
			// but cannot verify completion via inventory polling alone.
			return redfishInventoryVerification{}, nil
		}
	}

	if resolvedVersion == "" {
		return redfishInventoryVerification{}, nil
	}

	return redfishInventoryVerification{Updated: true}, nil
}

func redfishInventoryFailure(body map[string]interface{}) (bool, string) {
	statusMap, ok := body["Status"].(map[string]interface{})
	if !ok {
		return false, ""
	}

	health := strings.ToLower(strings.TrimSpace(asString(statusMap["Health"])))
	if conditions, ok := statusMap["Conditions"].([]interface{}); ok {
		for _, raw := range conditions {
			condition, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			severity := strings.ToLower(strings.TrimSpace(asString(condition["Severity"])))
			if severity == "warning" || severity == "critical" || health == "warning" || health == "critical" {
				for _, key := range []string{"Message", "MessageId", "Resolution"} {
					if detail := strings.TrimSpace(asString(condition[key])); detail != "" {
						return true, detail
					}
				}
				return true, fmt.Sprintf("Redfish inventory reported %s condition", severity)
			}
		}
	}

	if health == "warning" || health == "critical" {
		return true, fmt.Sprintf("Redfish inventory health is %s", health)
	}

	return false, ""
}

func getRedfishJSON(ctx context.Context, targetAddress, username, password, uri string) (map[string]interface{}, int, error) {
	endpoint := resolveRedfishEndpoint(targetAddress, uri)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build Redfish GET request: %w", err)
	}
	req.SetBasicAuth(username, password)

	resp, err := newRedfishHTTPClient().Do(req)
	if err != nil {
		if isLikelyTransientNetworkError(err) {
			return nil, 0, &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: err.Error()}
		}
		return nil, 0, fmt.Errorf("Redfish GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		return nil, resp.StatusCode, &firmwareproxy.HTTPStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("Redfish returned %s", resp.Status)}
	}
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode >= 500 {
		return nil, resp.StatusCode, &firmwareproxy.HTTPStatusError{StatusCode: 503, Message: fmt.Sprintf("Redfish returned %s", resp.Status)}
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("parse Redfish response: %w", err)
	}

	return body, resp.StatusCode, nil
}

func newRedfishHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func resolveRedfishEndpoint(targetAddress, uri string) string {
	uri = strings.TrimSpace(uri)
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		return uri
	}
	if strings.HasPrefix(uri, "/") {
		return fmt.Sprintf("https://%s%s", strings.TrimSpace(targetAddress), uri)
	}
	return fmt.Sprintf("https://%s/%s", strings.TrimSpace(targetAddress), uri)
}

func redfishTaskDetail(body map[string]interface{}) string {
	if message, ok := body["Message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}

	if messages, ok := body["Messages"].([]interface{}); ok {
		for _, raw := range messages {
			messageMap, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			for _, key := range []string{"Message", "MessageId", "Resolution"} {
				if value := strings.TrimSpace(asString(messageMap[key])); value != "" {
					return value
				}
			}
		}
	}

	return strings.TrimSpace(asString(body["TaskStatus"]))
}

func asString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
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

func loadBMCCredentials(secretID string) (bmcCredentials, error) {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return bmcCredentials{}, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: "spec.secretID is required"}
	}

	store := secretsruntime.GetStore()
	if store == nil {
		return bmcCredentials{}, fmt.Errorf("secret store is not initialized")
	}

	raw, err := store.GetSecretByID(secretID)
	if err != nil {
		return bmcCredentials{}, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: fmt.Sprintf("load credentials for secretID %q: %v", secretID, err)}
	}

	var creds bmcCredentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return bmcCredentials{}, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: fmt.Sprintf("decode credentials for secretID %q: %v", secretID, err)}
	}

	creds.Username = strings.TrimSpace(creds.Username)
	creds.Password = strings.TrimSpace(creds.Password)
	if creds.Username == "" || creds.Password == "" {
		return bmcCredentials{}, &firmwareproxy.HTTPStatusError{StatusCode: 400, Message: fmt.Sprintf("secretID %q must contain non-empty username and password", secretID)}
	}

	return creds, nil
}
