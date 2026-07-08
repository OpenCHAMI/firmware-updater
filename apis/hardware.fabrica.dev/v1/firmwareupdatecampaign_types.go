// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"fmt"
	"strings"

	"github.com/openchami/fabrica/pkg/fabrica"
)

const (
	CampaignStatePending             = "Pending"
	CampaignStateInProgress          = "InProgress"
	CampaignStateCompleted           = "Completed"
	CampaignStateCompletedWithErrors = "CompletedWithErrors"
	CampaignStateFailed              = "Failed"
)

const (
	CampaignUIDAnnotation      = "campaign-uid"
	CampaignTargetAnnotation   = "campaign-target"
	CampaignChildKeyAnnotation = "campaign-child-key"
)

// FirmwareUpdateCampaign represents a bulk update operation.
type FirmwareUpdateCampaign struct {
	APIVersion string                       `json:"apiVersion"`
	Kind       string                       `json:"kind"`
	Metadata   fabrica.Metadata             `json:"metadata"`
	Spec       FirmwareUpdateCampaignSpec   `json:"spec" validate:"required"`
	Status     FirmwareUpdateCampaignStatus `json:"status,omitempty"`
}

// FirmwareUpdateCampaignSpec defines shared update settings and target list.
type FirmwareUpdateCampaignSpec struct {
	ServerProxyAddress string                   `json:"serverProxyAddress" validate:"required"`
	Component          string                   `json:"component,omitempty"`
	Discovery          *DiscoverySpec           `json:"discovery,omitempty"`
	OCIReference       *string                  `json:"ociReference,omitempty"`
	Targets            []FirmwareCampaignTarget `json:"targets" validate:"required,dive"`
}

// FirmwareCampaignTarget identifies a single hardware target in a campaign.
type FirmwareCampaignTarget struct {
	TargetAddress string `json:"targetAddress" validate:"required"`
	SecretID      string `json:"secretID" validate:"required"`
}

// FirmwareUpdateCampaignStatus tracks aggregate and per-target state.
type FirmwareUpdateCampaignStatus struct {
	CampaignState string             `json:"campaignState,omitempty"`
	Summary       CampaignSummary    `json:"summary,omitempty"`
	ChildJobs     []CampaignChildJob `json:"childJobs,omitempty"`
}

// CampaignSummary provides aggregate counts for campaign progress.
type CampaignSummary struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

// CampaignChildJob captures linked child FirmwareUpdateJob status.
type CampaignChildJob struct {
	TargetAddress string `json:"targetAddress"`
	JobUID        string `json:"jobUID"`
	JobState      string `json:"jobState"`
	ErrorDetail   string `json:"errorDetail,omitempty"`
}

// Validate implements custom validation logic for FirmwareUpdateCampaign.
func (r *FirmwareUpdateCampaign) Validate(ctx context.Context) error {
	_ = ctx

	hasOCIReference := r.Spec.OCIReference != nil && strings.TrimSpace(*r.Spec.OCIReference) != ""
	hasDiscovery := r.Spec.Discovery != nil
	hasComponent := strings.TrimSpace(r.Spec.Component) != ""

	if r.Spec.OCIReference != nil && strings.TrimSpace(*r.Spec.OCIReference) == "" {
		return fmt.Errorf("spec.ociReference must not be empty when provided")
	}

	if hasDiscovery {
		if strings.TrimSpace(r.Spec.Discovery.Repository) == "" {
			return fmt.Errorf("spec.discovery.repository must be provided")
		}
		if hasComponent {
			if strings.TrimSpace(r.Spec.Discovery.HardwareModel) == "" {
				return fmt.Errorf("spec.discovery.hardwareModel must be provided when spec.component is set")
			}
			if strings.TrimSpace(r.Spec.Discovery.Version) == "" {
				return fmt.Errorf("spec.discovery.version must be provided when spec.component is set")
			}
		}
	}

	if hasOCIReference && hasDiscovery {
		return fmt.Errorf("spec.ociReference and spec.discovery are mutually exclusive")
	}

	if !hasOCIReference && !hasDiscovery {
		return fmt.Errorf("one of spec.ociReference or spec.discovery must be provided")
	}

	if hasOCIReference && !hasComponent {
		return fmt.Errorf("spec.component must be provided when spec.ociReference is used")
	}

	if len(r.Spec.Targets) == 0 {
		return fmt.Errorf("spec.targets must include at least one target")
	}

	seen := make(map[string]struct{}, len(r.Spec.Targets))
	for i, target := range r.Spec.Targets {
		if strings.TrimSpace(target.TargetAddress) == "" {
			return fmt.Errorf("spec.targets[%d].targetAddress must be provided", i)
		}
		if strings.TrimSpace(target.SecretID) == "" {
			return fmt.Errorf("spec.targets[%d].secretID must be provided", i)
		}

		key := strings.TrimSpace(target.TargetAddress)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("spec.targets[%d].targetAddress duplicates a previous target: %s", i, key)
		}
		seen[key] = struct{}{}
	}

	return nil
}

// GetKind returns the kind of the resource.
func (r *FirmwareUpdateCampaign) GetKind() string {
	return "FirmwareUpdateCampaign"
}

// GetName returns the name of the resource.
func (r *FirmwareUpdateCampaign) GetName() string {
	return r.Metadata.Name
}

// GetUID returns the UID of the resource.
func (r *FirmwareUpdateCampaign) GetUID() string {
	return r.Metadata.UID
}

// IsHub marks this as the hub/storage version.
func (r *FirmwareUpdateCampaign) IsHub() {}
