package v1

import (
	"context"
	"strings"
	"testing"
)

func TestFirmwareUpdateJobValidateSingleEndpoint(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		targets   []string
		wantError string
	}{
		{name: "discover one endpoint"},
		{name: "explicit endpoint", targets: []string{"/firmware/BMC"}},
		{name: "multiple endpoints", targets: []string{"/firmware/BMC", "/firmware/BIOS"}, wantError: "exactly one firmware endpoint"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reference := "registry.example.com/firmware:1.2.0"
			job := &FirmwareUpdateJob{Spec: FirmwareUpdateJobSpec{
				OCIReference: &reference,
				Component:    "BMC",
				Targets:      scenario.targets,
			}}
			err := job.Validate(context.Background())
			if scenario.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), scenario.wantError) {
				t.Fatalf("expected %q, got %v", scenario.wantError, err)
			}
		})
	}
}

func TestFirmwareUpdateJobValidateDiscoveryStillRequiresFields(t *testing.T) {
	job := &FirmwareUpdateJob{
		Spec: FirmwareUpdateJobSpec{
			TargetAddress:      "10.0.0.1",
			SecretID:           "bmc-secret",
			ServerProxyAddress: "127.0.0.1",
			Component:          "BMC",
			Discovery: &DiscoverySpec{
				Repository: "127.0.0.1:5000/firmware/cabinet",
			},
		},
	}

	err := job.Validate(context.Background())
	if err == nil || err.Error() != "spec.discovery.hardwareModel must be provided" {
		t.Fatalf("expected discovery hardwareModel validation error, got %v", err)
	}
}
