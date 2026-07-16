// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareUpdateCampaign.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/openchami/fabrica/pkg/events"
	"github.com/openchami/fabrica/pkg/resource"
	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

var campaignNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9-]`)
var hardwareHintTokenPattern = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._-]*`)
var hardwareHintModelPattern = regexp.MustCompile(`^[A-Za-z]+\d+`)

type desiredCampaignJob struct {
	key string
	job *v1.FirmwareUpdateJob
}

type inventoryComponent struct {
	Identifier       string
	TargetURI        string
	InstalledVersion string
	HardwareHints    []string
}

func (r *FirmwareUpdateCampaignReconciler) reconcileFirmwareUpdateCampaign(ctx context.Context, campaign *v1.FirmwareUpdateCampaign) error {
	if campaign.Status.CampaignState == "" {
		campaign.Status.CampaignState = v1.CampaignStatePending
	}

	jobsByKey, err := r.listCampaignJobs(ctx, campaign.Metadata.UID)
	if err != nil {
		return fmt.Errorf("list campaign child jobs: %w", err)
	}

	desiredJobs, err := r.desiredCampaignJobs(ctx, campaign)
	if err != nil {
		return fmt.Errorf("resolve desired campaign jobs: %w", err)
	}
	createdAny, err := r.reconcileDesiredCampaignJobs(ctx, desiredJobs, jobsByKey)
	if err != nil {
		return err
	}

	if createdAny && campaign.Status.CampaignState == v1.CampaignStatePending {
		campaign.Status.CampaignState = v1.CampaignStateInProgress
	}

	summary, childJobs := summarizeCampaignJobs(jobsByKey)

	// Adjust summary to account for desired jobs that are queued by the sequencer
	if len(desiredJobs) > summary.Total {
		summary.Pending += len(desiredJobs) - summary.Total
		summary.Total = len(desiredJobs)
	}

	campaign.Status.Summary = summary
	campaign.Status.ChildJobs = childJobs
	campaign.Status.CampaignState = deriveCampaignState(summary)

	return nil
}

func (r *FirmwareUpdateCampaignReconciler) reconcileDesiredCampaignJobs(ctx context.Context, desiredJobs []desiredCampaignJob, jobsByKey map[string]*v1.FirmwareUpdateJob) (bool, error) {
	sort.SliceStable(desiredJobs, func(i, j int) bool {
		return desiredJobs[i].key < desiredJobs[j].key
	})

	activeTargets := buildActiveCampaignTargets(jobsByKey)
	createdAny := false

	for _, desired := range desiredJobs {
		if _, exists := jobsByKey[desired.key]; exists {
			continue
		}

		targetAddress := campaignChildTargetAddress(desired.job)
		if activeTargets[targetAddress] {
			continue
		}

		job := desired.job
		uid, err := resource.GenerateUIDForResource("FirmwareUpdateJob")
		if err != nil {
			return false, fmt.Errorf("generate child FirmwareUpdateJob UID for child key %q: %w", desired.key, err)
		}
		job.Metadata.UID = uid
		if err := r.Client.Create(ctx, job); err != nil {
			return false, fmt.Errorf("create child FirmwareUpdateJob for child key %q: %w", desired.key, err)
		}
		if err := events.PublishResourceCreated(ctx, "FirmwareUpdateJob", job.Metadata.UID, job.Metadata.Name, job); err != nil {
			r.Logger.Warnf("Failed to publish created event for child FirmwareUpdateJob %s: %v", job.Metadata.UID, err)
		}

		jobsByKey[desired.key] = job
		activeTargets[targetAddress] = true
		createdAny = true
	}

	return createdAny, nil
}

func (r *FirmwareUpdateCampaignReconciler) desiredCampaignJobs(ctx context.Context, campaign *v1.FirmwareUpdateCampaign) ([]desiredCampaignJob, error) {
	if !isUniversalDiscoveryCampaign(campaign) {
		desired := make([]desiredCampaignJob, 0, len(campaign.Spec.Targets))
		for _, target := range campaign.Spec.Targets {
			key := defaultCampaignChildKey(target.TargetAddress)
			desired = append(desired, desiredCampaignJob{
				key: key,
				job: campaignToChildJob(campaign, target, key, campaign.Spec.OCIReference, campaign.Spec.Discovery, campaign.Spec.Component, nil),
			})
		}
		return desired, nil
	}

	if campaign.Spec.Discovery == nil {
		return nil, fmt.Errorf("universal discovery requires spec.discovery")
	}

	desired := make([]desiredCampaignJob, 0)
	for _, target := range campaign.Spec.Targets {
		creds, err := loadBMCCredentials(target.SecretID)
		if err != nil {
			return nil, fmt.Errorf("load credentials for target %q: %w", target.TargetAddress, err)
		}

		components, err := discoverInventoryComponentsWithBackoff(ctx, target.TargetAddress, creds.Username, creds.Password)
		if err != nil {
			return nil, fmt.Errorf("discover firmware inventory for target %q: %w", target.TargetAddress, err)
		}

		for _, component := range components {
			repositories := buildUniversalDiscoveryRepositories(campaign.Spec.Discovery.Repository, component.Identifier)
			resolved, updateAvailable, err := resolvePayloadFromInventoryRepositoriesWithBackoff(ctx, repositories, component.HardwareHints, component.InstalledVersion)
			if err != nil {
				if statusErr, ok := err.(*firmwareproxy.HTTPStatusError); ok && statusErr.StatusCode == http.StatusNotFound {
					continue
				}
				return nil, fmt.Errorf("resolve repository update for target %q component %q: %w", target.TargetAddress, component.Identifier, err)
			}
			if !updateAvailable {
				continue
			}

			key := universalCampaignChildKey(target.TargetAddress, component.TargetURI)
			targets := []string{component.TargetURI}
			componentName := component.Identifier
			ociReference := resolved.OCIReference
			desired = append(desired, desiredCampaignJob{
				key: key,
				job: campaignToChildJob(campaign, target, key, &ociReference, nil, componentName, targets),
			})
		}
	}

	return desired, nil
}

func campaignToChildJob(campaign *v1.FirmwareUpdateCampaign, target v1.FirmwareCampaignTarget, childKey string, ociReference *string, discovery *v1.DiscoverySpec, component string, targets []string) *v1.FirmwareUpdateJob {
	jobName := buildCampaignJobName(campaign.Metadata.Name, childKey)

	job := &v1.FirmwareUpdateJob{
		APIVersion: campaign.APIVersion,
		Kind:       "FirmwareUpdateJob",
		Metadata:   campaign.Metadata,
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress:      target.TargetAddress,
			SecretID:           target.SecretID,
			OCIReference:       ociReference,
			Discovery:          discovery,
			Targets:            append([]string(nil), targets...),
			Component:          component,
			ServerProxyAddress: campaign.Spec.ServerProxyAddress,
		},
	}

	job.Metadata.Name = jobName
	job.Metadata.UID = ""
	job.Metadata.Labels = copyMap(campaign.Metadata.Labels)
	job.Metadata.Annotations = copyMap(campaign.Metadata.Annotations)
	if job.Metadata.Annotations == nil {
		job.Metadata.Annotations = make(map[string]string)
	}
	job.Metadata.Annotations[v1.CampaignUIDAnnotation] = campaign.Metadata.UID
	job.Metadata.Annotations[v1.CampaignTargetAnnotation] = target.TargetAddress
	job.Metadata.Annotations[v1.CampaignChildKeyAnnotation] = childKey

	return job
}

func (r *FirmwareUpdateCampaignReconciler) listCampaignJobs(ctx context.Context, campaignUID string) (map[string]*v1.FirmwareUpdateJob, error) {
	items, err := r.Client.List(ctx, "FirmwareUpdateJob")
	if err != nil {
		return nil, err
	}

	jobsByTarget := make(map[string]*v1.FirmwareUpdateJob)
	for _, item := range items {
		job, ok := item.(*v1.FirmwareUpdateJob)
		if !ok {
			continue
		}
		if job.Metadata.Annotations[v1.CampaignUIDAnnotation] != campaignUID {
			continue
		}

		childKey := strings.TrimSpace(job.Metadata.Annotations[v1.CampaignChildKeyAnnotation])
		if childKey == "" {
			childKey = strings.TrimSpace(job.Metadata.Annotations[v1.CampaignTargetAnnotation])
		}
		if childKey == "" {
			childKey = strings.TrimSpace(job.Spec.TargetAddress)
		}
		if childKey == "" {
			continue
		}
		jobsByTarget[childKey] = job
	}

	return jobsByTarget, nil
}

func summarizeCampaignJobs(jobsByTarget map[string]*v1.FirmwareUpdateJob) (v1.CampaignSummary, []v1.CampaignChildJob) {
	out := v1.CampaignSummary{Total: len(jobsByTarget)}
	childJobs := make([]v1.CampaignChildJob, 0, len(jobsByTarget))

	targets := make([]string, 0, len(jobsByTarget))
	for target := range jobsByTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	for _, target := range targets {
		job := jobsByTarget[target]
		state := strings.TrimSpace(job.Status.JobState)
		if state == "" {
			state = v1.CampaignStatePending
		}

		switch state {
		case v1.CampaignStateCompleted:
			out.Completed++
		case v1.CampaignStateFailed:
			out.Failed++
		default:
			out.Pending++
		}

		childJobs = append(childJobs, v1.CampaignChildJob{
			TargetAddress: campaignChildTargetAddress(job),
			JobUID:        job.Metadata.UID,
			JobState:      state,
			ErrorDetail:   job.Status.ErrorDetail,
		})
	}

	return out, childJobs
}

func buildActiveCampaignTargets(jobsByKey map[string]*v1.FirmwareUpdateJob) map[string]bool {
	activeTargets := make(map[string]bool)
	for _, job := range jobsByKey {
		if !campaignJobIsActive(job) {
			continue
		}

		targetAddress := campaignChildTargetAddress(job)
		if targetAddress == "" {
			continue
		}
		activeTargets[targetAddress] = true
	}

	return activeTargets
}

func campaignJobIsActive(job *v1.FirmwareUpdateJob) bool {
	if job == nil {
		return false
	}

	state := strings.TrimSpace(job.Status.JobState)
	return state != v1.CampaignStateCompleted && state != v1.CampaignStateFailed
}

func deriveCampaignState(summary v1.CampaignSummary) string {
	if summary.Total == 0 {
		return v1.CampaignStatePending
	}
	if summary.Pending > 0 {
		return v1.CampaignStateInProgress
	}
	if summary.Completed == summary.Total {
		return v1.CampaignStateCompleted
	}
	if summary.Failed == summary.Total {
		return v1.CampaignStateFailed
	}
	if summary.Failed > 0 && summary.Completed > 0 {
		return v1.CampaignStateCompletedWithErrors
	}
	return v1.CampaignStateInProgress
}

func resolvePayloadFromInventoryWithBackoff(ctx context.Context, repository string, hardwareHints []string, installedVersion string) (firmwareproxy.DiscoveryResult, bool, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		resolved, updateAvailable, err := firmwareproxy.ResolvePayloadFromInventory(ctx, repository, hardwareHints, installedVersion)
		if err == nil {
			return resolved, updateAvailable, nil
		}

		lastErr = err
		if isTerminalError(err) || attempt == 4 {
			break
		}

		if waitErr := sleepWithContext(ctx, backoff); waitErr != nil {
			return firmwareproxy.DiscoveryResult{}, false, waitErr
		}
		backoff *= 2
	}

	return firmwareproxy.DiscoveryResult{}, false, lastErr
}

func resolvePayloadFromInventoryRepositoriesWithBackoff(ctx context.Context, repositories []string, hardwareHints []string, installedVersion string) (firmwareproxy.DiscoveryResult, bool, error) {
	var lastErr error
	foundNoUpdate := false

	for _, repository := range repositories {
		resolved, updateAvailable, err := resolvePayloadFromInventoryWithBackoff(ctx, repository, hardwareHints, installedVersion)
		if err == nil {
			if updateAvailable {
				return resolved, true, nil
			}
			foundNoUpdate = true
			return firmwareproxy.DiscoveryResult{}, false, nil
		}

		statusErr, ok := err.(*firmwareproxy.HTTPStatusError)
		if ok && statusErr.StatusCode == http.StatusNotFound {
			lastErr = err
			continue
		}

		return firmwareproxy.DiscoveryResult{}, false, err
	}

	if foundNoUpdate {
		return firmwareproxy.DiscoveryResult{}, false, nil
	}
	if lastErr != nil {
		return firmwareproxy.DiscoveryResult{}, false, lastErr
	}

	return firmwareproxy.DiscoveryResult{}, false, &firmwareproxy.HTTPStatusError{StatusCode: http.StatusNotFound, Message: "no compatible firmware manifests found"}
}

func discoverInventoryComponentsWithBackoff(ctx context.Context, targetAddress, username, password string) ([]inventoryComponent, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 1; attempt <= 4; attempt++ {
		components, err := discoverInventoryComponents(ctx, targetAddress, username, password)
		if err == nil {
			return components, nil
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

func discoverInventoryComponents(ctx context.Context, targetAddress, username, password string) ([]inventoryComponent, error) {
	client := newRedfishClient(targetAddress, username, password)
	inventory, _, err := client.GetJSON(ctx, "/redfish/v1/UpdateService/FirmwareInventory")
	if err != nil {
		return nil, err
	}

	members, ok := inventory["Members"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("auto-discovery failed: no Members array in FirmwareInventory response")
	}

	components := make([]inventoryComponent, 0, len(members))
	for _, member := range members {
		memberMap, ok := member.(map[string]interface{})
		if !ok {
			continue
		}

		memberID := strings.TrimSpace(asString(memberMap["@odata.id"]))
		if memberID == "" {
			continue
		}

		memberDetail, _, err := client.GetJSON(ctx, memberID)
		if err != nil {
			continue
		}

		identifier := firstNonEmptyString(
			stringValue(memberDetail["Id"]),
			stringValue(memberDetail["Name"]),
			memberID,
		)
		components = append(components, inventoryComponent{
			Identifier:       identifier,
			TargetURI:        memberID,
			InstalledVersion: stringValue(memberDetail["Version"]),
			HardwareHints:    collectHardwareHints(targetAddress, memberDetail),
		})
	}

	if len(components) == 0 {
		return nil, fmt.Errorf("auto-discovery failed: no readable firmware inventory members found")
	}

	return components, nil
}

func isUniversalDiscoveryCampaign(campaign *v1.FirmwareUpdateCampaign) bool {
	return campaign.Spec.OCIReference == nil && campaign.Spec.Discovery != nil && strings.TrimSpace(campaign.Spec.Component) == ""
}

func buildUniversalDiscoveryRepositories(baseRepository, componentIdentifier string) []string {
	base := strings.TrimSpace(baseRepository)
	if base == "" {
		return nil
	}

	seen := make(map[string]struct{})
	repositories := make([]string, 0, 3)
	add := func(repository string) {
		repository = strings.TrimSpace(repository)
		if repository == "" {
			return
		}
		if _, exists := seen[repository]; exists {
			return
		}
		seen[repository] = struct{}{}
		repositories = append(repositories, repository)
	}

	for _, slug := range componentRepositorySlugs(componentIdentifier) {
		add(strings.TrimRight(base, "/") + "/" + slug)
	}
	add(base)

	return repositories
}

func componentRepositorySlugs(componentIdentifier string) []string {
	trimmed := strings.TrimSpace(strings.ToLower(componentIdentifier))
	if trimmed == "" {
		return nil
	}

	slug := campaignNameSanitizer.ReplaceAllString(trimmed, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return nil
	}

	results := []string{slug}
	compact := strings.ReplaceAll(slug, "-", "")
	if compact != slug {
		results = append(results, compact)
	}

	return results
}

func defaultCampaignChildKey(targetAddress string) string {
	return strings.TrimSpace(targetAddress)
}

func universalCampaignChildKey(targetAddress, targetURI string) string {
	return fmt.Sprintf("%s|%s", strings.TrimSpace(targetAddress), strings.TrimSpace(targetURI))
}

func campaignChildTargetAddress(job *v1.FirmwareUpdateJob) string {
	if job == nil {
		return ""
	}

	targetAddress := strings.TrimSpace(job.Metadata.Annotations[v1.CampaignTargetAnnotation])
	if targetAddress != "" {
		return targetAddress
	}

	return strings.TrimSpace(job.Spec.TargetAddress)
}

func collectHardwareHints(targetAddress string, detail map[string]interface{}) []string {
	seen := make(map[string]struct{})
	hints := make([]string, 0)

	add := func(candidate string) {
		for _, token := range tokenizeHardwareHint(candidate) {
			if _, exists := seen[token]; exists {
				continue
			}
			seen[token] = struct{}{}
			hints = append(hints, token)
		}
	}

	for _, key := range []string{"Id", "Name", "Description", "Model", "SKU", "PartNumber", "SoftwareId", "@odata.id"} {
		add(stringValue(detail[key]))
	}
	add(targetAddress)
	collectStringValues(detail["RelatedItem"], add)

	return hints
}

func collectStringValues(value interface{}, add func(string)) {
	switch typed := value.(type) {
	case string:
		add(typed)
	case []interface{}:
		for _, item := range typed {
			collectStringValues(item, add)
		}
	case map[string]interface{}:
		for _, nested := range typed {
			collectStringValues(nested, add)
		}
	}
}

func tokenizeHardwareHint(candidate string) []string {
	trimmed := strings.ToLower(strings.TrimSpace(candidate))
	if trimmed == "" {
		return nil
	}

	seen := make(map[string]struct{})
	tokens := make([]string, 0)
	add := func(token string) {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			return
		}
		if _, exists := seen[token]; exists {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	add(trimmed)
	for _, token := range hardwareHintTokenPattern.FindAllString(trimmed, -1) {
		add(token)
		add(hardwareHintModelPattern.FindString(token))
	}

	return tokens
}

func stringValue(value interface{}) string {
	v, _ := value.(string)
	return strings.TrimSpace(v)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func copyMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func buildCampaignJobName(campaignName, targetAddress string) string {
	base := strings.TrimSpace(campaignName)
	if base == "" {
		base = "campaign"
	}
	base = campaignNameSanitizer.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "campaign"
	}

	target := strings.ToLower(strings.TrimSpace(targetAddress))
	target = campaignNameSanitizer.ReplaceAllString(target, "-")
	target = strings.Trim(target, "-")
	if target == "" {
		target = "target"
	}

	name := fmt.Sprintf("%s-%s", base, target)
	if len(name) > 63 {
		name = name[:63]
	}
	name = strings.Trim(name, "-")
	if name == "" {
		name = "campaign-target"
	}
	return name
}
