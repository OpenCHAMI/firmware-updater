// Copyright © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1
// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"testing"
)

func ptr(s string) *string { return &s }

func baseValidSingle() *FirmwareUpdateJob {
	return &FirmwareUpdateJob{
		Spec: FirmwareUpdateJobSpec{
			TargetAddress:      "10.0.0.1",
			Username:           "admin",
			Password:           "secret",
			OCIReference:       ptr("127.0.0.1:5000/firmware/bios:1.8.2"),
			Component:          "BIOS",
			ServerProxyAddress: "10.254.1.20",
		},
	}
}

func TestValidate_SingleTargetAddress_OK(t *testing.T) {
	if err := baseValidSingle().Validate(context.Background()); err != nil {
		t.Fatalf("expected valid single-target job, got error: %v", err)
	}
}

func TestValidate_GroupRef_OK(t *testing.T) {
	job := baseValidSingle()
	job.Spec.TargetAddress = ""
	job.Spec.GroupRef = "cabinet-x1000"
	job.Spec.MaxParallel = 5
	if err := job.Validate(context.Background()); err != nil {
		t.Fatalf("expected valid group job, got error: %v", err)
	}
}

func TestValidate_BothSelectors_Rejected(t *testing.T) {
	job := baseValidSingle()
	job.Spec.GroupRef = "cabinet-x1000"
	if err := job.Validate(context.Background()); err == nil {
		t.Fatal("expected error when both targetAddress and groupRef are set")
	}
}

func TestValidate_NeitherSelector_Rejected(t *testing.T) {
	job := baseValidSingle()
	job.Spec.TargetAddress = ""
	if err := job.Validate(context.Background()); err == nil {
		t.Fatal("expected error when neither targetAddress nor groupRef is set")
	}
}

func TestValidate_GroupRef_NoComponentOrTargets_Rejected(t *testing.T) {
	job := baseValidSingle()
	job.Spec.TargetAddress = ""
	job.Spec.GroupRef = "cabinet-x1000"
	job.Spec.Component = ""
	job.Spec.Targets = nil
	if err := job.Validate(context.Background()); err == nil {
		t.Fatal("expected error when neither targets nor component provided")
	}
}

func TestValidate_MaxParallelNegative_Rejected(t *testing.T) {
	job := baseValidSingle()
	job.Spec.TargetAddress = ""
	job.Spec.GroupRef = "cabinet-x1000"
	job.Spec.MaxParallel = -1
	if err := job.Validate(context.Background()); err == nil {
		t.Fatal("expected error when maxParallel is negative")
	}
}

func TestValidate_MaxParallelZero_OK(t *testing.T) {
	job := baseValidSingle()
	job.Spec.TargetAddress = ""
	job.Spec.GroupRef = "cabinet-x1000"
	job.Spec.MaxParallel = 0 // omitted -> defaults to serial at reconcile time
	if err := job.Validate(context.Background()); err != nil {
		t.Fatalf("expected maxParallel=0 (omitted) to be valid, got: %v", err)
	}
}
