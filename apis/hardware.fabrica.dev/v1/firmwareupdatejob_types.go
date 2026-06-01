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

const (
	FirmwareUpdateJobStatePending    = "Pending"
	FirmwareUpdateJobStateValidating = "Validating"
	FirmwareUpdateJobStateInProgress = "InProgress"
	FirmwareUpdateJobStateCompleted  = "Completed"
	FirmwareUpdateJobStateFailed     = "Failed"
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
	BundleName         string   `json:"bundleName" validate:"required"`
	Targets            []string `json:"targets" validate:"required,min=1,dive,required"`
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
	if strings.TrimSpace(r.Spec.TargetAddress) == "" {
		return fmt.Errorf("spec.targetAddress is required")
	}
	if strings.TrimSpace(r.Spec.Username) == "" {
		return fmt.Errorf("spec.username is required")
	}
	if strings.TrimSpace(r.Spec.Password) == "" {
		return fmt.Errorf("spec.password is required")
	}
	if strings.TrimSpace(r.Spec.BundleName) == "" {
		return fmt.Errorf("spec.bundleName is required")
	}
	if len(r.Spec.Targets) == 0 {
		return fmt.Errorf("spec.targets must contain at least one Redfish target URI")
	}
	for i, target := range r.Spec.Targets {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("spec.targets[%d] must not be empty", i)
		}
	}
	if strings.TrimSpace(r.Spec.ServerProxyAddress) == "" {
		return fmt.Errorf("spec.serverProxyAddress is required")
	}

	if r.Status.JobState == "" {
		return nil
	}
	switch r.Status.JobState {
	case FirmwareUpdateJobStatePending,
		FirmwareUpdateJobStateValidating,
		FirmwareUpdateJobStateInProgress,
		FirmwareUpdateJobStateCompleted,
		FirmwareUpdateJobStateFailed:
		return nil
	default:
		return fmt.Errorf("status.jobState must be one of Pending, Validating, InProgress, Completed, Failed")
	}
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
