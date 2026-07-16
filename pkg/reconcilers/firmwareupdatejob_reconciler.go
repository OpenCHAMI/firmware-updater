// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareUpdateJob.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/openchami/fabrica/pkg/events"
	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/internal/secretsruntime"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
	"github.com/user/firmware-updater/pkg/redfish"
	"golang.org/x/mod/semver"
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

	client := redfish.NewClient(res.Spec.TargetAddress, creds.Username, creds.Password)
	body, headers, _, err := client.PostJSON(ctx, actionURI, payload)
	if err != nil {
		return "", err
	}

	taskID := strings.TrimSpace(headers.Get("Location"))
	if taskID == "" {
		if v, ok := body["@odata.id"].(string); ok {
			taskID = strings.TrimSpace(v)
		} else if v, ok := body["TaskID"].(string); ok {
			taskID = strings.TrimSpace(v)
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

var (
	redfishLongPollMaxDuration = 30 * time.Minute
	redfishLongPollMinInterval = 15 * time.Second
	redfishLongPollMaxInterval = 30 * time.Second
	semverTokenPattern         = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)
)

func pollRedfishTaskWithBackoff(ctx context.Context, targetAddress, username, password, taskID string) (redfishTaskObservation, error) {
	deadline := time.Now().Add(redfishLongPollMaxDuration)
	var lastErr error

	for attempt := 1; time.Now().Before(deadline); attempt++ {
		observation, err := pollRedfishTaskOnce(ctx, targetAddress, username, password, taskID)
		if err != nil {
			lastErr = err
			if isTerminalError(err) {
				return redfishTaskObservation{}, err
			}
		} else {
			switch observation.State {
			case redfishTaskStateRunning:
				// Keep polling.
			case redfishTaskStateCompleted, redfishTaskStateFailed, redfishTaskStateMissing:
				return observation, nil
			}
		}

		if waitErr := sleepWithContext(ctx, redfishLongPollInterval(attempt)); waitErr != nil {
			return redfishTaskObservation{}, waitErr
		}
	}

	if lastErr != nil {
		return redfishTaskObservation{}, lastErr
	}

	return redfishTaskObservation{}, fmt.Errorf("timed out waiting for Redfish task %q", strings.TrimSpace(taskID))
}

func pollRedfishTaskOnce(ctx context.Context, targetAddress, username, password, taskID string) (redfishTaskObservation, error) {
	body, statusCode, err := getRedfishJSON(ctx, targetAddress, username, password, taskID)
	if err != nil {
		var statusErr *redfish.Error
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
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
		if taskStatus == "critical" {
			return redfishTaskObservation{State: redfishTaskStateFailed, Detail: detail}, nil
		}
		if taskStatus == "warning" && redfishTaskHasTransitionalWarning(body) {
			return redfishTaskObservation{State: redfishTaskStateRunning, Detail: detail}, nil
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
		if taskStatus == "critical" {
			return redfishTaskObservation{State: redfishTaskStateFailed, Detail: detail}, nil
		}
		if taskStatus == "warning" && redfishTaskHasTransitionalWarning(body) {
			return redfishTaskObservation{State: redfishTaskStateRunning, Detail: detail}, nil
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
	deadline := time.Now().Add(redfishLongPollMaxDuration)
	var lastErr error

	for attempt := 1; time.Now().Before(deadline); attempt++ {
		verification, err := verifyFirmwareTargetsUpdatedOnce(ctx, res, creds)
		if err != nil {
			lastErr = err
			if isTerminalError(err) {
				return redfishInventoryVerification{}, err
			}
		} else {
			if verification.Failed || verification.Updated {
				return verification, nil
			}
		}

		if waitErr := sleepWithContext(ctx, redfishLongPollInterval(attempt)); waitErr != nil {
			return redfishInventoryVerification{}, waitErr
		}
	}

	if lastErr != nil {
		return redfishInventoryVerification{}, lastErr
	}

	return redfishInventoryVerification{}, fmt.Errorf("timed out verifying Redfish firmware inventory update")
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

	resolvedVersion := strings.TrimSpace(res.Status.ResolvedVersion)

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
			installedVersion := strings.TrimSpace(asString(body["Version"]))
			if installedVersion == "" || !versionsSemanticallyEqual(installedVersion, resolvedVersion) {
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
			if severity == "critical" || health == "critical" {
				for _, key := range []string{"Message", "MessageId", "Resolution"} {
					if detail := strings.TrimSpace(asString(condition[key])); detail != "" {
						return true, detail
					}
				}
				return true, fmt.Sprintf("Redfish inventory reported %s condition", severity)
			}
		}
	}

	if health == "critical" {
		return true, fmt.Sprintf("Redfish inventory health is %s", health)
	}

	return false, ""
}

func getRedfishJSON(ctx context.Context, targetAddress, username, password, uri string) (map[string]interface{}, int, error) {
	client := redfish.NewClient(targetAddress, username, password)
	return client.GetJSON(ctx, uri)
}

func redfishTaskDetail(body map[string]interface{}) string {
	if messages, ok := body["Messages"].([]interface{}); ok {
		if detail := preferredRedfishTaskMessage(messages); detail != "" {
			return detail
		}
	}

	if message, ok := body["Message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}

	return strings.TrimSpace(asString(body["TaskStatus"]))
}

func preferredRedfishTaskMessage(messages []interface{}) string {
	bestScore := -1
	bestDetail := ""
	for _, raw := range messages {
		messageMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		messageID := strings.TrimSpace(asString(messageMap["MessageId"]))
		message := strings.TrimSpace(asString(messageMap["Message"]))
		resolution := strings.TrimSpace(asString(messageMap["Resolution"]))

		score := redfishTaskMessageScore(messageID, message)
		detail := firstNonEmptyTaskDetail(messageID, message, resolution)
		if detail == "" {
			continue
		}

		if score > bestScore {
			bestScore = score
			bestDetail = detail
		}
	}

	return bestDetail
}

func redfishTaskMessageScore(messageID, message string) int {
	combined := strings.ToLower(strings.TrimSpace(messageID + " " + message))
	score := 0
	if messageID != "" {
		score += 2
	}
	for _, token := range []string{"critical", "error", "fail", "timeout", "aborted", "exception"} {
		if strings.Contains(combined, token) {
			score += 4
		}
	}
	for _, token := range []string{"warning", "reboot", "pending"} {
		if strings.Contains(combined, token) {
			score += 1
		}
	}
	return score
}

func firstNonEmptyTaskDetail(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func redfishTaskHasTransitionalWarning(body map[string]interface{}) bool {
	messages, ok := body["Messages"].([]interface{})
	if !ok {
		return false
	}

	for _, raw := range messages {
		messageMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		messageID := strings.ToLower(strings.TrimSpace(asString(messageMap["MessageId"])))
		message := strings.ToLower(strings.TrimSpace(asString(messageMap["Message"])))
		combined := messageID + " " + message
		for _, token := range []string{"pendingreboot", "reboot", "resetrequired", "restartrequired", "pending"} {
			if strings.Contains(combined, token) {
				return true
			}
		}
	}

	return false
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
	var proxyStatusErr *firmwareproxy.HTTPStatusError
	if errors.As(err, &proxyStatusErr) {
		return proxyStatusErr.StatusCode >= 400 && proxyStatusErr.StatusCode < 500
	}

	var redfishStatusErr *redfish.Error
	if errors.As(err, &redfishStatusErr) {
		return redfishStatusErr.IsClientError()
	}

	return false
}

// discoverUpdateServiceAction queries the UpdateService endpoint and returns the SimpleUpdate action URI
func discoverUpdateServiceAction(ctx context.Context, targetAddress, username, password string) (string, error) {
	client := redfish.NewClient(targetAddress, username, password)
	return client.DiscoverUpdateServiceAction(ctx)
}

// discoverTargetsFromInventory queries FirmwareInventory and returns targets matching the component
func discoverTargetsFromInventory(ctx context.Context, targetAddress, username, password, component string) ([]string, error) {
	client := redfish.NewClient(targetAddress, username, password)
	return client.DiscoverTargetsFromInventory(ctx, component)
}

func redfishLongPollInterval(attempt int) time.Duration {
	interval := redfishLongPollMinInterval + (time.Duration(attempt-1) * 5 * time.Second)
	if interval > redfishLongPollMaxInterval {
		return redfishLongPollMaxInterval
	}
	return interval
}

func versionsSemanticallyEqual(installedVersion, resolvedVersion string) bool {
	installedNormalized, ok := normalizeSemver(installedVersion)
	if !ok {
		return false
	}

	resolvedNormalized, ok := normalizeSemver(resolvedVersion)
	if !ok {
		return false
	}

	return semver.Compare(installedNormalized, resolvedNormalized) == 0
}

func normalizeSemver(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}

	if normalized, ok := canonicalizeSemverCandidate(trimmed); ok {
		return normalized, true
	}

	token := semverTokenPattern.FindString(trimmed)
	if token == "" {
		return "", false
	}

	return canonicalizeSemverCandidate(token)
}

func canonicalizeSemverCandidate(candidate string) (string, bool) {
	withPrefix := candidate
	if !strings.HasPrefix(withPrefix, "v") {
		withPrefix = "v" + withPrefix
	}
	if !semver.IsValid(withPrefix) {
		return "", false
	}
	return semver.Canonical(withPrefix), true
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
