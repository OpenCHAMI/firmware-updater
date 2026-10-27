// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import "testing"

func TestCredentialsSpecValidateAcceptsNilAndEmpty(t *testing.T) {
	var nilSpec *CredentialsSpec
	if err := nilSpec.Validate("spec.credentials"); err != nil {
		t.Fatalf("expected nil credentials section to validate, got %v", err)
	}

	empty := &CredentialsSpec{}
	if err := empty.Validate("spec.credentials"); err != nil {
		t.Fatalf("expected empty credentials section to validate, got %v", err)
	}
}

func TestCredentialsSpecValidateRejectsStructuralProblems(t *testing.T) {
	cases := []struct {
		name    string
		spec    *CredentialsSpec
		wantErr string
	}{
		{
			name:    "missing secretID",
			spec:    &CredentialsSpec{Bindings: []CredentialBinding{{Nodes: []string{"x9000-1"}}}},
			wantErr: "spec.credentials.bindings[0].secretID must be provided",
		},
		{
			name:    "no nodes",
			spec:    &CredentialsSpec{Bindings: []CredentialBinding{{SecretID: "bmc"}}},
			wantErr: "spec.credentials.bindings[0].nodes must include at least one entry",
		},
		{
			name:    "empty node entry",
			spec:    &CredentialsSpec{Bindings: []CredentialBinding{{Nodes: []string{"  "}, SecretID: "bmc"}}},
			wantErr: "spec.credentials.bindings[0].nodes[0] must not be empty",
		},
		{
			name:    "malformed glob",
			spec:    &CredentialsSpec{Bindings: []CredentialBinding{{Nodes: []string{"x9000-[a"}, SecretID: "bmc"}}},
			wantErr: "spec.credentials.bindings[0].nodes[0] is not a valid node pattern: syntax error in pattern",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate("spec.credentials")
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestCredentialsSpecMatchNode(t *testing.T) {
	spec := &CredentialsSpec{
		Bindings: []CredentialBinding{
			{Nodes: []string{"x9000-*"}, SecretID: "glob-secret"},
			{Nodes: []string{"x9000-42", "x9000-43"}, SecretID: "exact-secret"},
			{Nodes: []string{"*"}, SecretID: "catch-all"},
		},
	}

	cases := []struct {
		name   string
		node   string
		want   string
		wantOK bool
	}{
		{name: "exact beats earlier glob", node: "x9000-42", want: "exact-secret", wantOK: true},
		{name: "glob match", node: "x9000-99", want: "glob-secret", wantOK: true},
		{name: "case insensitive exact", node: "X9000-43", want: "exact-secret", wantOK: true},
		{name: "first glob wins", node: "other-node", want: "catch-all", wantOK: true},
		{name: "empty node never matches", node: "   ", want: "", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := spec.MatchNode(tc.node)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("MatchNode(%q) = (%q, %v), want (%q, %v)", tc.node, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestCredentialsSpecMatchNodeNilReceiver(t *testing.T) {
	var spec *CredentialsSpec
	if got, ok := spec.MatchNode("x9000-1"); ok || got != "" {
		t.Fatalf("expected nil credentials section to match nothing, got (%q, %v)", got, ok)
	}
}

func TestCredentialsSpecMatchNodeNoBindings(t *testing.T) {
	spec := &CredentialsSpec{Default: "fallback"}
	if got, ok := spec.MatchNode("x9000-1"); ok || got != "" {
		t.Fatalf("expected no binding match, got (%q, %v)", got, ok)
	}
}
