// Copyright © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"fmt"

	"github.com/openchami/fabrica/pkg/fabrica"
)

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
	TargetAddress      string   `json:"targetAddress" validate:"required"`
	Username           string   `json:"username" validate:"required"`
	Password           string   `json:"password" validate:"required"`
	OCIReference       string   `json:"ociReference" validate:"required"`
	Targets            []string `json:"targets,omitempty" validate:"dive,required"`
	Component          string   `json:"component,omitempty"`
	ServerProxyAddress string   `json:"serverProxyAddress" validate:"required"`
}

// FirmwareUpdateJobStatus defines the observed state of FirmwareUpdateJob
type FirmwareUpdateJobStatus struct {
	JobState    string `json:"jobState,omitempty"`
	TaskID      string `json:"taskID,omitempty"`
	ErrorDetail string `json:"errorDetail,omitempty"`
}

// Validate implements custom validation logic for FirmwareUpdateJob
func (r *FirmwareUpdateJob) Validate(ctx context.Context) error {
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
