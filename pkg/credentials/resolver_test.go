// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package credentials

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

// fakeStore mimics the Magellan secret store closely enough for resolution:
// a missing ID yields an error, matching the on-disk store's behaviour.
type fakeStore struct {
	secrets map[string]string
}

func newFakeStore(ids ...string) fakeStore {
	store := fakeStore{secrets: make(map[string]string, len(ids))}
	for _, id := range ids {
		store.secrets[id] = `{"username":"root","password":"initial0"}`
	}
	return store
}

func (f fakeStore) GetSecretByID(secretID string) (string, error) {
	value, ok := f.secrets[secretID]
	if !ok {
		return "", fmt.Errorf("secret %q not found", secretID)
	}
	return value, nil
}

func assertBadRequest(t *testing.T, err error, wantFragment string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error containing %q, got nil", wantFragment)
	}

	var statusErr *firmwareproxy.HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected *firmwareproxy.HTTPStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != 400 {
		t.Fatalf("expected status 400, got %d", statusErr.StatusCode)
	}
	if !strings.Contains(statusErr.Message, wantFragment) {
		t.Fatalf("expected message containing %q, got %q", wantFragment, statusErr.Message)
	}
}

func TestResolveExplicitWins(t *testing.T) {
	store := newFakeStore("explicit-secret", "x9000-1", "binding-secret", "fallback")

	got, err := Resolve(store, ResolveInput{
		NodeName:   "x9000-1",
		ExplicitID: "explicit-secret",
		Credentials: &v1.CredentialsSpec{
			Bindings: []v1.CredentialBinding{{Nodes: []string{"x9000-*"}, SecretID: "binding-secret"}},
			Default:  "fallback",
		},
		ServerDefault: "fallback",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "explicit-secret" || got.Source != SourceExplicit {
		t.Fatalf("got %+v, want explicit-secret/%s", got, SourceExplicit)
	}
}

func TestResolveExplicitMissingIsTerminalAndDoesNotFallBack(t *testing.T) {
	store := newFakeStore("x9000-1", "fallback")

	_, err := Resolve(store, ResolveInput{
		NodeName:      "x9000-1",
		ExplicitID:    "typo-secret",
		ServerDefault: "fallback",
	})
	assertBadRequest(t, err, `spec.secretID selected secretID "typo-secret"`)
}

func TestResolveBindingExactBeatsGlob(t *testing.T) {
	store := newFakeStore("glob-secret", "exact-secret")

	got, err := Resolve(store, ResolveInput{
		NodeName: "x9000-42",
		Credentials: &v1.CredentialsSpec{
			Bindings: []v1.CredentialBinding{
				{Nodes: []string{"x9000-*"}, SecretID: "glob-secret"},
				{Nodes: []string{"x9000-42"}, SecretID: "exact-secret"},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "exact-secret" || got.Source != SourceBinding {
		t.Fatalf("got %+v, want exact-secret/%s", got, SourceBinding)
	}
}

func TestResolveBindingOrderDecidesBetweenGlobs(t *testing.T) {
	store := newFakeStore("first-glob", "second-glob")

	got, err := Resolve(store, ResolveInput{
		NodeName: "x9000-42",
		Credentials: &v1.CredentialsSpec{
			Bindings: []v1.CredentialBinding{
				{Nodes: []string{"x9000-*"}, SecretID: "first-glob"},
				{Nodes: []string{"x*"}, SecretID: "second-glob"},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "first-glob" {
		t.Fatalf("got %+v, want first-glob", got)
	}
}

func TestResolveBindingBeatsNodeNameLookup(t *testing.T) {
	store := newFakeStore("binding-secret", "x9000-42")

	got, err := Resolve(store, ResolveInput{
		NodeName: "x9000-42",
		Credentials: &v1.CredentialsSpec{
			Bindings: []v1.CredentialBinding{{Nodes: []string{"x9000-*"}, SecretID: "binding-secret"}},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "binding-secret" || got.Source != SourceBinding {
		t.Fatalf("got %+v, want binding-secret/%s", got, SourceBinding)
	}
}

func TestResolveBindingWithMissingSecretIsTerminal(t *testing.T) {
	store := newFakeStore("x9000-42", "fallback")

	_, err := Resolve(store, ResolveInput{
		NodeName: "x9000-42",
		Credentials: &v1.CredentialsSpec{
			Bindings: []v1.CredentialBinding{{Nodes: []string{"x9000-*"}, SecretID: "gone"}},
			Default:  "fallback",
		},
	})
	assertBadRequest(t, err, `credentials binding for node "x9000-42" selected secretID "gone"`)
}

func TestResolveNodeNameLookup(t *testing.T) {
	store := newFakeStore("x9000-42")

	got, err := Resolve(store, ResolveInput{NodeName: "x9000-42"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "x9000-42" || got.Source != SourceNodeName {
		t.Fatalf("got %+v, want x9000-42/%s", got, SourceNodeName)
	}
}

func TestResolveSpecDefaultBeatsServerDefault(t *testing.T) {
	store := newFakeStore("spec-fallback", "server-fallback")

	got, err := Resolve(store, ResolveInput{
		NodeName:      "unknown-node",
		Credentials:   &v1.CredentialsSpec{Default: "spec-fallback"},
		ServerDefault: "server-fallback",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "spec-fallback" || got.Source != SourceSpecDefault {
		t.Fatalf("got %+v, want spec-fallback/%s", got, SourceSpecDefault)
	}
}

func TestResolveServerDefault(t *testing.T) {
	store := newFakeStore("server-fallback")

	got, err := Resolve(store, ResolveInput{
		NodeName:      "unknown-node",
		ServerDefault: "server-fallback",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SecretID != "server-fallback" || got.Source != SourceServerDefault {
		t.Fatalf("got %+v, want server-fallback/%s", got, SourceServerDefault)
	}
}

func TestResolveNothingConfigured(t *testing.T) {
	store := newFakeStore("some-other-secret")

	_, err := Resolve(store, ResolveInput{NodeName: "unknown-node"})
	assertBadRequest(t, err, `no credential could be resolved for node "unknown-node"`)
}

func TestResolveMalformedGlobIsTerminal(t *testing.T) {
	store := newFakeStore("fallback")

	_, err := Resolve(store, ResolveInput{
		NodeName: "x9000-1",
		Credentials: &v1.CredentialsSpec{
			Bindings: []v1.CredentialBinding{{Nodes: []string{"x9000-[a"}, SecretID: "fallback"}},
		},
	})
	assertBadRequest(t, err, "is not a valid node pattern")
}

func TestResolveWithoutStore(t *testing.T) {
	_, err := Resolve(nil, ResolveInput{NodeName: "x9000-1", ExplicitID: "any"})
	if err == nil || err.Error() != "secret store is not initialized" {
		t.Fatalf("expected uninitialized store error, got %v", err)
	}

	var statusErr *firmwareproxy.HTTPStatusError
	if errors.As(err, &statusErr) {
		t.Fatal("an uninitialized store is transient and must not be reported as a client error")
	}
}

func TestExists(t *testing.T) {
	store := fakeStore{secrets: map[string]string{"present": "payload", "blank": "   "}}

	cases := []struct {
		name     string
		store    SecretLookup
		secretID string
		want     bool
	}{
		{name: "present", store: store, secretID: "present", want: true},
		{name: "trims input", store: store, secretID: "  present  ", want: true},
		{name: "absent", store: store, secretID: "absent", want: false},
		{name: "blank payload", store: store, secretID: "blank", want: false},
		{name: "empty id", store: store, secretID: "", want: false},
		{name: "nil store", store: nil, secretID: "present", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Exists(tc.store, tc.secretID); got != tc.want {
				t.Fatalf("Exists(%q) = %v, want %v", tc.secretID, got, tc.want)
			}
		})
	}
}

func TestServerDefaultSecretIDRoundTrip(t *testing.T) {
	original := ServerDefaultSecretID()
	t.Cleanup(func() { SetServerDefaultSecretID(original) })

	SetServerDefaultSecretID("  fleet-default  ")
	if got := ServerDefaultSecretID(); got != "fleet-default" {
		t.Fatalf("ServerDefaultSecretID() = %q, want %q", got, "fleet-default")
	}

	SetServerDefaultSecretID("")
	if got := ServerDefaultSecretID(); got != "" {
		t.Fatalf("expected empty default to disable the rule, got %q", got)
	}
}
