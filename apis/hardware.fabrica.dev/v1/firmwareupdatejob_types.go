// Copyright © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"fmt"
	"strings"

	"github.com/openchami/fabrica/pkg/fabrica"
)

// DiscoverySpec defines OCI artifact discovery parameters.
type DiscoverySpec struct {
	Repository    string `json:"repository" yaml:"repository" validate:"required"`
	HardwareModel string `json:"hardwareModel" yaml:"hardwareModel" validate:"required"`
	Version       string `json:"version" yaml:"version" validate:"required"`
}

// FirmwareUpdateJob represents a firmwareupdatejob resource
type FirmwareUpdateJob struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   fabrica.Metadata        `json:"metadata"`
	Spec       FirmwareUpdateJobSpec   `json:"spec" validate:"required"`
	Status     FirmwareUpdateJobStatus `json:"status,omitempty"`
}

// FirmwareUpdateJobSpec defines the desired state of FirmwareUpdateJob
type FirmwareUpdateJobSpec struct {
	TargetAddress      string         `json:"targetAddress" validate:"required"`
	SecretID           string         `json:"secretID" validate:"required"`
	OCIReference       *string        `json:"ociReference,omitempty"`
	Discovery          *DiscoverySpec `json:"discovery,omitempty"`
	Targets            []string       `json:"targets,omitempty" validate:"dive,required"`
	Component          string         `json:"component,omitempty"`
	IgnoreLocks        bool           `json:"ignoreLocks,omitempty"`
	ServerProxyAddress string         `json:"serverProxyAddress" validate:"required"`
}

// FirmwareUpdateJobStatus defines the observed state of FirmwareUpdateJob
type FirmwareUpdateJobStatus struct {
	JobState        string   `json:"jobState,omitempty"`
	TaskID          string   `json:"taskID,omitempty"`
	ErrorDetail     string   `json:"errorDetail,omitempty"`
	ResolvedVersion string   `json:"resolvedVersion,omitempty"`
	ResolvedDigest  string   `json:"resolvedDigest,omitempty"`
	LockConflicts   []string `json:"lockConflicts,omitempty"`
}

// Validate implements custom validation logic for FirmwareUpdateJob
func (r *FirmwareUpdateJob) Validate(ctx context.Context) error {
	hasOCIReference := r.Spec.OCIReference != nil && strings.TrimSpace(*r.Spec.OCIReference) != ""
	hasDiscovery := r.Spec.Discovery != nil

	if r.Spec.OCIReference != nil && strings.TrimSpace(*r.Spec.OCIReference) == "" {
		return fmt.Errorf("spec.ociReference must not be empty when provided")
	}

	if hasDiscovery {
		if strings.TrimSpace(r.Spec.Discovery.Repository) == "" {
			return fmt.Errorf("spec.discovery.repository must be provided")
		}
		if strings.TrimSpace(r.Spec.Discovery.HardwareModel) == "" {
			return fmt.Errorf("spec.discovery.hardwareModel must be provided")
		}
		if strings.TrimSpace(r.Spec.Discovery.Version) == "" {
			return fmt.Errorf("spec.discovery.version must be provided")
		}
	}

	if hasOCIReference == hasDiscovery {
		return fmt.Errorf("exactly one of spec.ociReference or spec.discovery must be provided")
	}

	// Either Targets or Component must be provided
	if len(r.Spec.Targets) == 0 && r.Spec.Component == "" {
		return fmt.Errorf("spec.targets or spec.component must be provided")
	}

	// If Targets is provided, validate it
	if len(r.Spec.Targets) > 0 {
		for i, target := range r.Spec.Targets {
			if target == "" {
				return fmt.Errorf("spec.targets[%d] must not be empty", i)
			}
		}
	}

	return nil
}

// GetKind returns the kind of the resource
func (r *FirmwareUpdateJob) GetKind() string {
	return "FirmwareUpdateJob"
}

// GetName returns the name of the resource
func (r *FirmwareUpdateJob) GetName() string {
	return r.Metadata.Name
}

// GetUID returns the UID of the resource
func (r *FirmwareUpdateJob) GetUID() string {
	return r.Metadata.UID
}

// IsHub marks this as the hub/storage version
func (r *FirmwareUpdateJob) IsHub() {}
