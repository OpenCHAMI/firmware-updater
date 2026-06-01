// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
// This file contains user-customizable reconciliation logic for FirmwareBundle.
//
// ⚠️ This file is safe to edit - it will NOT be overwritten by code generation.
package reconcilers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"
)

// reconcileFirmwareBundle contains custom reconciliation logic.
//
// This method is called by the generated Reconcile() orchestration method.
// Implement FirmwareBundle-specific reconciliation logic here.
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
//   - res: The FirmwareBundle resource to reconcile
//
// Returns:
//   - error: If reconciliation failed (will trigger retry with backoff)
func (r *FirmwareBundleReconciler) reconcileFirmwareBundle(ctx context.Context, res *v1.FirmwareBundle) error {
	if err := v1.ValidateRegistryURLFormat(res.Spec.RegistryURL); err != nil {
		res.Status.Discovered = false
		res.Status.Error = err.Error()
		return nil
	}
	if err := v1.ValidateRepositoryFormat(res.Spec.Repository); err != nil {
		res.Status.Discovered = false
		res.Status.Error = err.Error()
		return nil
	}
	if err := v1.ValidateTagOrDigestFormat(res.Spec.TagOrDigest); err != nil {
		res.Status.Discovered = false
		res.Status.Error = err.Error()
		return nil
	}

	key := res.Spec.RegistryURL + "/" + res.Spec.Repository + "@" + res.Spec.TagOrDigest
	sum := sha256.Sum256([]byte(key))

	res.Status.Discovered = true
	res.Status.ManifestDigest = "sha256:" + hex.EncodeToString(sum[:])
	res.Status.Error = ""
	res.Status.ExtractedMetadata = map[string]string{
		"artifactType": "application/vnd.oci.image.manifest.v1+json",
		"source":       "phase1-mock",
		"bundle":       res.Metadata.Name,
		"repository":   res.Spec.Repository,
		"reference":    res.Spec.TagOrDigest,
	}

	r.Logger.Infof("FirmwareBundle %s discovered using mock metadata extraction", res.GetUID())
	_ = ctx

	return nil
}
