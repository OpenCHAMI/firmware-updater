// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareUpdateJob.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"fmt"
	"strings"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"
)

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
	if res.Status.JobState == v1.FirmwareUpdateJobStateInProgress ||
		res.Status.JobState == v1.FirmwareUpdateJobStateCompleted ||
		res.Status.JobState == v1.FirmwareUpdateJobStateFailed {
		r.Logger.Debugf("Skipping idempotent FirmwareUpdateJob %s in terminal/active state %s", res.GetUID(), res.Status.JobState)
		return nil
	}

	if err := r.validateFirmwareUpdateJobSpec(ctx, res); err != nil {
		res.Status.JobState = v1.FirmwareUpdateJobStateFailed
		res.Status.ErrorDetail = err.Error()
		return nil
	}

	if res.Status.JobState == "" {
		res.Status.JobState = v1.FirmwareUpdateJobStatePending
	}

	if res.Status.JobState == v1.FirmwareUpdateJobStatePending {
		res.Status.JobState = v1.FirmwareUpdateJobStateValidating
		res.Status.ErrorDetail = ""
		r.Logger.Infof("FirmwareUpdateJob %s transitioned to Validating", res.GetUID())
	}

	return nil
}

func (r *FirmwareUpdateJobReconciler) validateFirmwareUpdateJobSpec(ctx context.Context, res *v1.FirmwareUpdateJob) error {
	if strings.TrimSpace(res.Spec.TargetAddress) == "" {
		return fmt.Errorf("spec.targetAddress is required")
	}
	if strings.TrimSpace(res.Spec.Username) == "" {
		return fmt.Errorf("spec.username is required")
	}
	if strings.TrimSpace(res.Spec.Password) == "" {
		return fmt.Errorf("spec.password is required")
	}
	if strings.TrimSpace(res.Spec.BundleName) == "" {
		return fmt.Errorf("spec.bundleName is required")
	}
	if strings.TrimSpace(res.Spec.ServerProxyAddress) == "" {
		return fmt.Errorf("spec.serverProxyAddress is required")
	}
	if len(res.Spec.Targets) == 0 {
		return fmt.Errorf("spec.targets must contain at least one value")
	}
	for i, target := range res.Spec.Targets {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("spec.targets[%d] must not be empty", i)
		}
	}

	bundles, err := r.Client.List(ctx, "FirmwareBundle")
	if err != nil {
		return fmt.Errorf("failed to list FirmwareBundle resources: %w", err)
	}

	for _, item := range bundles {
		bundle, ok := item.(*v1.FirmwareBundle)
		if !ok {
			continue
		}
		if bundle.Metadata.Name == res.Spec.BundleName {
			return nil
		}
	}

	return fmt.Errorf("spec.bundleName %q does not reference an existing FirmwareBundle", res.Spec.BundleName)
}
