package v1

import (
	"context"
	"testing"
)

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
