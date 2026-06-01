package reconcilers

import (
	"context"
	"strings"
	"testing"

	v1 "firmware-manager/apis/hardware.fabrica.dev/v1"

	"github.com/openchami/fabrica/pkg/reconcile"
)

func TestReconcileFirmwareBundle(t *testing.T) {
	t.Parallel()

	reconciler := &FirmwareBundleReconciler{
		BaseReconciler: reconcile.BaseReconciler{Logger: reconcile.NewDefaultLogger()},
	}

	tests := []struct {
		name                 string
		resource             *v1.FirmwareBundle
		expectDiscovered     bool
		expectErrorSubstring string
		expectMetadata       bool
	}{
		{
			name: "valid bundle produces mock metadata",
			resource: &v1.FirmwareBundle{
				Metadata: v1.FirmwareBundle{}.Metadata,
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "registry.example.org",
					Repository:  "firmware/hpe/cray-ex-node-bmc",
					TagOrDigest: "v2.14.7",
				},
			},
			expectDiscovered: true,
			expectMetadata:   true,
		},
		{
			name: "invalid registry fails validation",
			resource: &v1.FirmwareBundle{
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "https://registry.example.org",
					Repository:  "firmware/hpe",
					TagOrDigest: "v2.14.7",
				},
			},
			expectDiscovered:     false,
			expectErrorSubstring: "registryURL",
		},
		{
			name: "invalid repository fails validation",
			resource: &v1.FirmwareBundle{
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "registry.example.org",
					Repository:  "firmware//hpe",
					TagOrDigest: "v2.14.7",
				},
			},
			expectDiscovered:     false,
			expectErrorSubstring: "repository",
		},
		{
			name: "invalid tag or digest fails validation",
			resource: &v1.FirmwareBundle{
				Spec: v1.FirmwareBundleSpec{
					RegistryURL: "registry.example.org",
					Repository:  "firmware/hpe",
					TagOrDigest: "not a valid ref",
				},
			},
			expectDiscovered:     false,
			expectErrorSubstring: "tagOrDigest",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := reconciler.reconcileFirmwareBundle(context.Background(), tt.resource)
			if err != nil {
				t.Fatalf("reconcileFirmwareBundle() returned unexpected error: %v", err)
			}

			if tt.resource.Status.Discovered != tt.expectDiscovered {
				t.Fatalf("expected Discovered=%v, got %v", tt.expectDiscovered, tt.resource.Status.Discovered)
			}

			if tt.expectErrorSubstring == "" && tt.resource.Status.Error != "" {
				t.Fatalf("expected empty status error, got %q", tt.resource.Status.Error)
			}
			if tt.expectErrorSubstring != "" && !strings.Contains(tt.resource.Status.Error, tt.expectErrorSubstring) {
				t.Fatalf("expected status error to contain %q, got %q", tt.expectErrorSubstring, tt.resource.Status.Error)
			}

			if tt.expectMetadata {
				if tt.resource.Status.ManifestDigest == "" {
					t.Fatal("expected manifest digest to be populated")
				}
				if len(tt.resource.Status.ExtractedMetadata) == 0 {
					t.Fatal("expected extracted metadata to be populated")
				}
			}
		})
	}
}
