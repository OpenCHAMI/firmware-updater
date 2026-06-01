package reconcilers

import (
	"context"
	"errors"
	"strings"
	"testing"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"

	"github.com/openchami/fabrica/pkg/reconcile"
)

type fakeClient struct {
	listByKind map[string][]interface{}
	listErr    error
}

func (f *fakeClient) Get(ctx context.Context, kind, uid string) (interface{}, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClient) List(ctx context.Context, kind string) ([]interface{}, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listByKind[kind], nil
}

func (f *fakeClient) Update(ctx context.Context, resource interface{}) error {
	return nil
}

func (f *fakeClient) Create(ctx context.Context, resource interface{}) error {
	return nil
}

func (f *fakeClient) Delete(ctx context.Context, kind, uid string) error {
	return nil
}

func TestReconcileFirmwareUpdateJob(t *testing.T) {
	t.Parallel()

	validBundle := &v1.FirmwareBundle{}
	validBundle.Metadata.Name = "bundle-1"

	reconciler := &FirmwareUpdateJobReconciler{
		BaseReconciler: reconcile.BaseReconciler{
			Client: &fakeClient{listByKind: map[string][]interface{}{"FirmwareBundle": {validBundle}}},
			Logger: reconcile.NewDefaultLogger(),
		},
	}

	baseJob := &v1.FirmwareUpdateJob{
		Spec: v1.FirmwareUpdateJobSpec{
			TargetAddress:      "10.0.0.5",
			Username:           "admin",
			Password:           "secret",
			BundleName:         "bundle-1",
			Targets:            []string{"/redfish/v1/UpdateService/FirmwareInventory/BMC"},
			ServerProxyAddress: "127.0.0.1",
		},
	}

	cloneBaseJob := func() *v1.FirmwareUpdateJob {
		j := *baseJob
		j.Spec.Targets = append([]string(nil), baseJob.Spec.Targets...)
		return &j
	}

	tests := []struct {
		name                   string
		job                    *v1.FirmwareUpdateJob
		expectState            string
		expectErrorSubstring   string
		expectErrorDetailEmpty bool
	}{
		{
			name: "pending transitions to validating",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Status.JobState = v1.FirmwareUpdateJobStatePending
				return j
			}(),
			expectState:            v1.FirmwareUpdateJobStateValidating,
			expectErrorDetailEmpty: true,
		},
		{
			name:                   "empty state initializes and transitions to validating",
			job:                    cloneBaseJob(),
			expectState:            v1.FirmwareUpdateJobStateValidating,
			expectErrorDetailEmpty: true,
		},
		{
			name: "terminal state remains unchanged",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Status.JobState = v1.FirmwareUpdateJobStateCompleted
				return j
			}(),
			expectState:            v1.FirmwareUpdateJobStateCompleted,
			expectErrorDetailEmpty: true,
		},
		{
			name: "missing targets marks job failed",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Spec.Targets = nil
				return j
			}(),
			expectState:          v1.FirmwareUpdateJobStateFailed,
			expectErrorSubstring: "targets",
		},
		{
			name: "unknown bundle marks job failed",
			job: func() *v1.FirmwareUpdateJob {
				j := cloneBaseJob()
				j.Spec.BundleName = "does-not-exist"
				return j
			}(),
			expectState:          v1.FirmwareUpdateJobStateFailed,
			expectErrorSubstring: "does not reference an existing FirmwareBundle",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := reconciler.reconcileFirmwareUpdateJob(context.Background(), tt.job)
			if err != nil {
				t.Fatalf("reconcileFirmwareUpdateJob() returned unexpected error: %v", err)
			}

			if tt.job.Status.JobState != tt.expectState {
				t.Fatalf("expected state %q, got %q", tt.expectState, tt.job.Status.JobState)
			}

			if tt.expectErrorSubstring != "" && !strings.Contains(tt.job.Status.ErrorDetail, tt.expectErrorSubstring) {
				t.Fatalf("expected error detail to contain %q, got %q", tt.expectErrorSubstring, tt.job.Status.ErrorDetail)
			}

			if tt.expectErrorDetailEmpty && tt.job.Status.ErrorDetail != "" {
				t.Fatalf("expected empty error detail, got %q", tt.job.Status.ErrorDetail)
			}
		})
	}
}
