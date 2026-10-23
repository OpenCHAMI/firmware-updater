// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"fmt"
	"path"
	"strings"
)

// CredentialBinding maps a set of node names to the stored secret that holds
// their BMC credentials.
type CredentialBinding struct {
	// Nodes lists the node names this binding applies to. A node name is the
	// FirmwareUpdateJob spec.targetAddress or the FirmwareUpdateCampaign
	// spec.targets[].targetAddress. Entries may be exact names or glob
	// patterns (for example "x9000-*"); matching is case-insensitive.
	Nodes []string `json:"nodes" yaml:"nodes" validate:"required,min=1,dive,required"`

	// SecretID identifies the stored credential used for the matched nodes.
	SecretID string `json:"secretID" yaml:"secretID" validate:"required"`
}

// CredentialsSpec is an optional per-resource credential mapping. It lets a job
// or campaign point specific nodes at specific stored secrets without repeating
// a secretID on every target, and supplies a local fallback secret.
//
// It participates in the resolution chain implemented by pkg/credentials:
// explicit secretID, then bindings, then a secret stored under the node name,
// then this Default, then the server-wide default.
type CredentialsSpec struct {
	// Bindings are evaluated in declaration order and the first match wins.
	// Exact node-name matches are always preferred over glob matches,
	// regardless of declaration order.
	Bindings []CredentialBinding `json:"bindings,omitempty" yaml:"bindings,omitempty" validate:"omitempty,dive"`

	// Default is the secret used when no binding matches and no secret is
	// stored under the node name. It overrides the server-wide default.
	Default string `json:"default,omitempty" yaml:"default,omitempty"`
}

// Validate reports structural problems with the credentials section. fieldPath
// is the dotted path of this section in the owning resource (for example
// "spec.credentials") and is used to build actionable error messages.
//
// A nil receiver is valid: the section is optional.
func (c *CredentialsSpec) Validate(fieldPath string) error {
	if c == nil {
		return nil
	}

	for i, binding := range c.Bindings {
		if strings.TrimSpace(binding.SecretID) == "" {
			return fmt.Errorf("%s.bindings[%d].secretID must be provided", fieldPath, i)
		}
		if len(binding.Nodes) == 0 {
			return fmt.Errorf("%s.bindings[%d].nodes must include at least one entry", fieldPath, i)
		}
		for j, node := range binding.Nodes {
			trimmed := strings.TrimSpace(node)
			if trimmed == "" {
				return fmt.Errorf("%s.bindings[%d].nodes[%d] must not be empty", fieldPath, i, j)
			}
			// path.Match only reports ErrBadPattern for malformed patterns, so
			// matching against an empty name is enough to syntax-check it.
			if _, err := path.Match(trimmed, ""); err != nil {
				return fmt.Errorf("%s.bindings[%d].nodes[%d] is not a valid node pattern: %w", fieldPath, i, j, err)
			}
		}
	}

	return nil
}

// MatchNode returns the secret ID bound to nodeName and whether a binding
// matched. Exact matches take precedence over glob matches; within each pass
// the first matching binding wins.
func (c *CredentialsSpec) MatchNode(nodeName string) (string, bool) {
	if c == nil {
		return "", false
	}

	needle := strings.ToLower(strings.TrimSpace(nodeName))
	if needle == "" {
		return "", false
	}

	for _, binding := range c.Bindings {
		for _, node := range binding.Nodes {
			if strings.ToLower(strings.TrimSpace(node)) == needle {
				return strings.TrimSpace(binding.SecretID), true
			}
		}
	}

	for _, binding := range c.Bindings {
		for _, node := range binding.Nodes {
			pattern := strings.ToLower(strings.TrimSpace(node))
			if matched, err := path.Match(pattern, needle); err == nil && matched {
				return strings.TrimSpace(binding.SecretID), true
			}
		}
	}

	return "", false
}
