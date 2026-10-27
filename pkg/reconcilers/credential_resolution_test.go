// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package reconcilers

import (
	"strings"
	"testing"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/pkg/credentials"
)

func TestResolveBMCCredentialsChain(t *testing.T) {
	ensureTestSecretStore(t)

	original := credentials.ServerDefaultSecretID()
	credentials.SetServerDefaultSecretID("fleet-default")
	t.Cleanup(func() { credentials.SetServerDefaultSecretID(original) })

	cases := []struct {
		name       string
		nodeName   string
		explicitID string
		spec       *v1.CredentialsSpec
		wantSecret string
		wantSource credentials.Source
	}{
		{
			name:       "explicit secretID",
			nodeName:   "10.0.0.1",
			explicitID: "test-secret",
			wantSecret: "test-secret",
			wantSource: credentials.SourceExplicit,
		},
		{
			name:     "credentials binding",
			nodeName: "10.0.0.1",
			spec: &v1.CredentialsSpec{
				Bindings: []v1.CredentialBinding{{Nodes: []string{"10.0.0.*"}, SecretID: "binding-secret"}},
			},
			wantSecret: "binding-secret",
			wantSource: credentials.SourceBinding,
		},
		{
			name:       "node name lookup",
			nodeName:   "10.0.0.77",
			wantSecret: "10.0.0.77",
			wantSource: credentials.SourceNodeName,
		},
		{
			name:       "spec default",
			nodeName:   "10.0.0.1",
			spec:       &v1.CredentialsSpec{Default: "binding-secret"},
			wantSecret: "binding-secret",
			wantSource: credentials.SourceSpecDefault,
		},
		{
			name:       "server default",
			nodeName:   "10.0.0.1",
			wantSecret: "fleet-default",
			wantSource: credentials.SourceServerDefault,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			creds, resolution, err := resolveBMCCredentials(tc.nodeName, tc.explicitID, tc.spec)
			if err != nil {
				t.Fatalf("resolveBMCCredentials: %v", err)
			}
			if resolution.SecretID != tc.wantSecret || resolution.Source != tc.wantSource {
				t.Fatalf("got %+v, want %s/%s", resolution, tc.wantSecret, tc.wantSource)
			}
			if creds.Username != "root" || creds.Password != "initial0" {
				t.Fatalf("unexpected decoded credentials for %s", tc.name)
			}
		})
	}
}

func TestResolveBMCCredentialsUnresolvedIsTerminal(t *testing.T) {
	ensureTestSecretStore(t)

	original := credentials.ServerDefaultSecretID()
	credentials.SetServerDefaultSecretID("")
	t.Cleanup(func() { credentials.SetServerDefaultSecretID(original) })

	_, _, err := resolveBMCCredentials("10.0.0.254", "", nil)
	if err == nil {
		t.Fatal("expected an error when nothing resolves")
	}
	if !isTerminalError(err) {
		t.Fatalf("expected a terminal error so the job fails fast, got %v", err)
	}
	if !strings.Contains(err.Error(), "no credential could be resolved") {
		t.Fatalf("expected an actionable message, got %v", err)
	}
}

func TestResolveBMCCredentialsRejectsIncompleteSecret(t *testing.T) {
	ensureTestSecretStore(t)

	_, _, err := resolveBMCCredentials("10.0.0.1", "blank-secret", nil)
	if err == nil || !strings.Contains(err.Error(), "non-empty username and password") {
		t.Fatalf("expected incomplete-credential error, got %v", err)
	}
}

func TestCampaignToChildJobInheritsCampaignCredentials(t *testing.T) {
	spec := &v1.CredentialsSpec{
		Bindings: []v1.CredentialBinding{{Nodes: []string{"10.0.0.*"}, SecretID: "binding-secret"}},
		Default:  "fleet-default",
	}
	campaign := &v1.FirmwareUpdateCampaign{
		Spec: v1.FirmwareUpdateCampaignSpec{
			ServerProxyAddress: "127.0.0.1",
			Credentials:        spec,
		},
	}
	target := v1.FirmwareCampaignTarget{TargetAddress: "10.0.0.10"}

	child := campaignToChildJob(campaign, target, "10.0.0.10", strPtr("registry.example.com/fw/image:v1"), nil, "BMC", nil)

	if child.Spec.Credentials != spec {
		t.Fatalf("expected child job to inherit the campaign credentials section, got %+v", child.Spec.Credentials)
	}
	if child.Spec.SecretID != "" {
		t.Fatalf("expected child job to carry the empty target secretID, got %q", child.Spec.SecretID)
	}
}
