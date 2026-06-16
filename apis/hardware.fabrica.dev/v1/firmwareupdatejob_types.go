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
	Targets            []string `json:"targets" validate:"required,min=1,dive,required"`
	ServerProxyAddress string   `json:"serverProxyAddress" validate:"required"`
	UpdateURI          string   `json:"updateURI,omitempty"`
}

// FirmwareUpdateJobStatus defines the observed state of FirmwareUpdateJob
type FirmwareUpdateJobStatus struct {
	JobState    string `json:"jobState,omitempty"`
	TaskID      string `json:"taskID,omitempty"`
	ErrorDetail string `json:"errorDetail,omitempty"`
}

// Validate implements custom validation logic for FirmwareUpdateJob
func (r *FirmwareUpdateJob) Validate(ctx context.Context) error {
	if len(r.Spec.Targets) == 0 {
		return fmt.Errorf("spec.targets must contain at least one Redfish target URI")
	}

	for i, target := range r.Spec.Targets {
		if target == "" {
			return fmt.Errorf("spec.targets[%d] must not be empty", i)
		}
	}

	if r.Spec.UpdateURI != "" && !strings.HasPrefix(r.Spec.UpdateURI, "/redfish/v1/") {
		return fmt.Errorf("invalid UpdateURI: must begin with /redfish/v1/")
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
