// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"testing"
)

func TestFirmwareUpdateCampaignValidateUniversalDiscovery(t *testing.T) {
	repositoryOnly := &FirmwareUpdateCampaign{
		Spec: FirmwareUpdateCampaignSpec{
			ServerProxyAddress: "127.0.0.1",
			Discovery: &DiscoverySpec{
				Repository: "127.0.0.1:5000/firmware/cabinet",
			},
			Targets: []FirmwareCampaignTarget{{TargetAddress: "10.0.0.1", SecretID: "bmc-secret"}},
		},
	}

	if err := repositoryOnly.Validate(context.Background()); err != nil {
		if got := err.Error(); got != "" {
			t.Fatalf("expected repository-only universal discovery to validate, got error: %v", err)
		}
	}
}

func TestFirmwareUpdateCampaignValidateExplicitModeRequiresComponent(t *testing.T) {
	ref := "127.0.0.1:5000/firmware/cabinet:bmc"
	campaign := &FirmwareUpdateCampaign{
		Spec: FirmwareUpdateCampaignSpec{
			ServerProxyAddress: "127.0.0.1",
			OCIReference:       &ref,
			Targets:            []FirmwareCampaignTarget{{TargetAddress: "10.0.0.1", SecretID: "bmc-secret"}},
		},
	}

	err := campaign.Validate(context.Background())
	if err == nil || err.Error() != "spec.component must be provided when spec.ociReference is used" {
		t.Fatalf("expected missing component validation error, got %v", err)
	}
}

func TestFirmwareUpdateCampaignValidateSemiTargetedRequiresDiscoveryFields(t *testing.T) {
	campaign := &FirmwareUpdateCampaign{
		Spec: FirmwareUpdateCampaignSpec{
			ServerProxyAddress: "127.0.0.1",
			Component:          "BMC",
			Discovery: &DiscoverySpec{
				Repository: "127.0.0.1:5000/firmware/cabinet",
			},
			Targets: []FirmwareCampaignTarget{{TargetAddress: "10.0.0.1", SecretID: "bmc-secret"}},
		},
	}

	err := campaign.Validate(context.Background())
	if err == nil || err.Error() != "spec.discovery.hardwareModel must be provided when spec.component is set" {
		t.Fatalf("expected missing hardwareModel validation error, got %v", err)
	}
}