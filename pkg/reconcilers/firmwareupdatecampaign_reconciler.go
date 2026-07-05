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
	"regexp"
	"sort"
	"strings"

	"github.com/openchami/fabrica/pkg/resource"
	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
)

var campaignNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9-]`)

func (r *FirmwareUpdateCampaignReconciler) reconcileFirmwareUpdateCampaign(ctx context.Context, campaign *v1.FirmwareUpdateCampaign) error {
	if campaign.Status.CampaignState == "" {
		campaign.Status.CampaignState = v1.CampaignStatePending
	}

	jobsByTarget, err := r.listCampaignJobs(ctx, campaign.Metadata.UID)
	if err != nil {
		return fmt.Errorf("list campaign child jobs: %w", err)
	}

	createdAny := false
	for _, target := range campaign.Spec.Targets {
		targetKey := strings.TrimSpace(target.TargetAddress)
		if _, exists := jobsByTarget[targetKey]; exists {
			continue
		}

		job := campaignToChildJob(campaign, target)
		uid, err := resource.GenerateUIDForResource("FirmwareUpdateJob")
		if err != nil {
			return fmt.Errorf("generate child FirmwareUpdateJob UID for target %q: %w", target.TargetAddress, err)
		}
		job.Metadata.UID = uid
		if err := r.Client.Create(ctx, job); err != nil {
			return fmt.Errorf("create child FirmwareUpdateJob for target %q: %w", target.TargetAddress, err)
		}
		jobsByTarget[targetKey] = job
		createdAny = true
	}

	if createdAny && campaign.Status.CampaignState == v1.CampaignStatePending {
		campaign.Status.CampaignState = v1.CampaignStateInProgress
	}

	summary, childJobs := summarizeCampaignJobs(jobsByTarget)
	campaign.Status.Summary = summary
	campaign.Status.ChildJobs = childJobs
	campaign.Status.CampaignState = deriveCampaignState(summary)

	return nil
}

func campaignToChildJob(campaign *v1.FirmwareUpdateCampaign, target v1.FirmwareCampaignTarget) *v1.FirmwareUpdateJob {
	jobName := buildCampaignJobName(campaign.Metadata.Name, target.TargetAddress)

	job := &v1.FirmwareUpdateJob{
		APIVersion: campaign.APIVersion,
		Kind:       "FirmwareUpdateJob",
		Metadata:   campaign.Metadata,
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress:      target.TargetAddress,
			SecretID:           target.SecretID,
			OCIReference:       campaign.Spec.OCIReference,
			Discovery:          campaign.Spec.Discovery,
			Component:          campaign.Spec.Component,
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

		targetAddress := strings.TrimSpace(job.Metadata.Annotations[v1.CampaignTargetAnnotation])
		if targetAddress == "" {
			targetAddress = strings.TrimSpace(job.Spec.TargetAddress)
		}
		if targetAddress == "" {
			continue
		}
		jobsByTarget[targetAddress] = job
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
			TargetAddress: target,
			JobUID:        job.Metadata.UID,
			JobState:      state,
			ErrorDetail:   job.Status.ErrorDetail,
		})
	}

	return out, childJobs
}

func deriveCampaignState(summary v1.CampaignSummary) string {
	if summary.Total == 0 {
		return v1.CampaignStatePending
	}
	if summary.Completed == summary.Total {
		return v1.CampaignStateCompleted
	}
	if summary.Failed > 0 && (summary.Completed+summary.Failed) == summary.Total {
		return v1.CampaignStateFailed
	}
	return v1.CampaignStateInProgress
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
