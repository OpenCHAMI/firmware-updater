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
	// TargetAddress selects a single BMC. It is mutually exclusive with GroupRef
	// (exactly one must be set; enforced in Validate). It is no longer marked
	// required at the struct-tag level so that group mode can omit it.
	TargetAddress string `json:"targetAddress,omitempty"`
	// GroupRef selects the set of BMCs via an SMD user-defined group label.
	// Mutually exclusive with TargetAddress.
	GroupRef string `json:"groupRef,omitempty"`
	// MaxParallel bounds the number of member BMCs updated concurrently in group
	// mode. Only meaningful when GroupRef is set. If omitted it defaults to 1
	// (serial) at reconcile time. Must be >= 1 when provided.
	MaxParallel int `json:"maxParallel,omitempty"`
	// AllowPartialTargets controls group resolution strictness. When false
	// (default), any unresolvable member fails the job. When true, unresolvable
	// members are recorded and the job proceeds with the resolvable members.
	AllowPartialTargets bool           `json:"allowPartialTargets,omitempty"`
	Username            string         `json:"username" validate:"required"`
	Password            string         `json:"password" validate:"required"`
	OCIReference        *string        `json:"ociReference,omitempty"`
	Discovery           *DiscoverySpec `json:"discovery,omitempty"`
	Targets             []string       `json:"targets,omitempty" validate:"dive,required"`
	Component           string         `json:"component,omitempty"`
	ServerProxyAddress  string         `json:"serverProxyAddress" validate:"required"`
}

// FirmwareUpdateJobStatus defines the observed state of FirmwareUpdateJob
type FirmwareUpdateJobStatus struct {
	JobState        string `json:"jobState,omitempty"`
	TaskID          string `json:"taskID,omitempty"`
	ErrorDetail     string `json:"errorDetail,omitempty"`
	ResolvedVersion string `json:"resolvedVersion,omitempty"`
	ResolvedDigest  string `json:"resolvedDigest,omitempty"`
	// ResolutionDetail captures group member resolution details for debugging
	// (e.g. "resolved 5 of 5 members").
	ResolutionDetail string `json:"resolutionDetail,omitempty"`
	// MemberCount is the number of member BMCs selected (1 in single-TargetAddress
	// mode, N in group mode after de-duplication).
	MemberCount int `json:"memberCount,omitempty"`
	// CompletedCount is the number of member BMCs successfully dispatched in a
	// fan-out job.
	CompletedCount int `json:"completedCount,omitempty"`
	// FailedMembers lists member BMC addresses that failed during fan-out.
	FailedMembers []string `json:"failedMembers,omitempty"`
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

	// BMC selector exclusivity: exactly one of TargetAddress or GroupRef.
	hasTargetAddress := strings.TrimSpace(r.Spec.TargetAddress) != ""
	hasGroupRef := strings.TrimSpace(r.Spec.GroupRef) != ""
	if hasTargetAddress == hasGroupRef {
		return fmt.Errorf("exactly one of spec.targetAddress or spec.groupRef must be provided")
	}

	// MaxParallel, when provided, must be >= 1. It is only meaningful in group
	// mode. Zero is the unset value (omitempty) and defaults to serial at
	// reconcile time; negative values are rejected.
	if r.Spec.MaxParallel < 0 {
		return fmt.Errorf("spec.maxParallel must be >= 1 when provided")
	}

	// Either Targets or Component must be provided (applies to every resolved member).
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
