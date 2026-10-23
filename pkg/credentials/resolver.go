// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package credentials resolves which stored secret holds the BMC credentials
// for a given node.
//
// Resolution walks an ordered chain and stops at the first rule that produces a
// secret ID:
//
//  1. explicit       - spec.secretID / spec.targets[].secretID
//  2. binding        - a matching entry in the credentials section
//  3. node-name      - a secret stored under the node name itself
//  4. spec-default   - credentials.default on the job or campaign
//  5. server-default - the process-wide --default-secret-id
//
// Every rule except node-name requires the selected secret to be present in the
// store; a dangling reference is reported as a client error rather than being
// silently skipped, so a typo never falls through to the wrong credential.
package credentials

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

// Source names the resolution rule that selected a secret ID.
type Source string

const (
	SourceExplicit      Source = "explicit"
	SourceBinding       Source = "binding"
	SourceNodeName      Source = "node-name"
	SourceSpecDefault   Source = "spec-default"
	SourceServerDefault Source = "server-default"
)

// SecretLookup is the subset of the secret store the resolver depends on.
// The Magellan secrets.SecretStore satisfies it.
type SecretLookup interface {
	GetSecretByID(secretID string) (string, error)
}

// Resolution is the outcome of a successful resolution. It never carries
// credential material, only the selected identifier and the rule that picked it.
type Resolution struct {
	SecretID string
	Source   Source
}

// ResolveInput carries everything the chain needs for a single node.
type ResolveInput struct {
	// NodeName is the target BMC address, used for node-name lookup and for
	// matching credential bindings.
	NodeName string

	// ExplicitID is the secretID declared on the job or campaign target.
	ExplicitID string

	// Credentials is the optional credentials section of the owning resource.
	Credentials *v1.CredentialsSpec

	// ServerDefault is the process-wide fallback secret ID, normally
	// ServerDefaultSecretID().
	ServerDefault string
}

var (
	serverDefaultMu sync.RWMutex
	serverDefault   string
)

// SetServerDefaultSecretID records the process-wide fallback secret ID. An
// empty value disables the server-default rule.
func SetServerDefaultSecretID(secretID string) {
	serverDefaultMu.Lock()
	defer serverDefaultMu.Unlock()
	serverDefault = strings.TrimSpace(secretID)
}

// ServerDefaultSecretID returns the process-wide fallback secret ID, or "" when
// none is configured.
func ServerDefaultSecretID() string {
	serverDefaultMu.RLock()
	defer serverDefaultMu.RUnlock()
	return serverDefault
}

// Exists reports whether store holds a non-empty secret under secretID.
func Exists(store SecretLookup, secretID string) bool {
	secretID = strings.TrimSpace(secretID)
	if store == nil || secretID == "" {
		return false
	}

	raw, err := store.GetSecretByID(secretID)
	return err == nil && strings.TrimSpace(raw) != ""
}

// Resolve walks the resolution chain and returns the selected secret ID.
//
// Unresolvable input is reported as a 400 firmwareproxy.HTTPStatusError so that
// reconcilers treat it as terminal instead of retrying forever.
func Resolve(store SecretLookup, in ResolveInput) (Resolution, error) {
	if store == nil {
		return Resolution{}, fmt.Errorf("secret store is not initialized")
	}

	nodeName := strings.TrimSpace(in.NodeName)

	if explicit := strings.TrimSpace(in.ExplicitID); explicit != "" {
		return confirm(store, explicit, SourceExplicit, "spec.secretID")
	}

	if err := in.Credentials.Validate("spec.credentials"); err != nil {
		return Resolution{}, badRequest(err.Error())
	}

	if bound, ok := in.Credentials.MatchNode(nodeName); ok {
		return confirm(store, bound, SourceBinding, fmt.Sprintf("credentials binding for node %q", nodeName))
	}

	if Exists(store, nodeName) {
		return Resolution{SecretID: nodeName, Source: SourceNodeName}, nil
	}

	if in.Credentials != nil {
		if specDefault := strings.TrimSpace(in.Credentials.Default); specDefault != "" {
			return confirm(store, specDefault, SourceSpecDefault, "spec.credentials.default")
		}
	}

	if server := strings.TrimSpace(in.ServerDefault); server != "" {
		return confirm(store, server, SourceServerDefault, "default-secret-id")
	}

	return Resolution{}, badRequest(fmt.Sprintf(
		"no credential could be resolved for node %q: no explicit secretID, no matching credentials binding, no secret stored under the node name, and no default configured",
		nodeName))
}

// confirm verifies the selected secret is actually present before accepting it.
func confirm(store SecretLookup, secretID string, source Source, origin string) (Resolution, error) {
	if !Exists(store, secretID) {
		return Resolution{}, badRequest(fmt.Sprintf(
			"%s selected secretID %q, which is not present in the secret store", origin, secretID))
	}
	return Resolution{SecretID: secretID, Source: source}, nil
}

func badRequest(message string) error {
	return &firmwareproxy.HTTPStatusError{StatusCode: http.StatusBadRequest, Message: message}
}
