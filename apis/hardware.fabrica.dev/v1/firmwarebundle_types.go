// Copyright © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package v1

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/openchami/fabrica/pkg/fabrica"
)

var (
	firmwareRepositorySegmentPattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)
	firmwareTagPattern               = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	firmwareDigestPattern            = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// FirmwareBundle represents a firmwarebundle resource
type FirmwareBundle struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Metadata   fabrica.Metadata     `json:"metadata"`
	Spec       FirmwareBundleSpec   `json:"spec" validate:"required"`
	Status     FirmwareBundleStatus `json:"status,omitempty"`
}

// FirmwareBundleSpec defines the desired state of FirmwareBundle
type FirmwareBundleSpec struct {
	RegistryURL       string `json:"registryURL" validate:"required"`
	Repository        string `json:"repository" validate:"required"`
	TagOrDigest       string `json:"tagOrDigest" validate:"required"`
	CredentialsSecret string `json:"credentialsSecret,omitempty"`
}

// FirmwareBundleStatus defines the observed state of FirmwareBundle
type FirmwareBundleStatus struct {
	Discovered        bool              `json:"discovered"`
	ManifestDigest    string            `json:"manifestDigest,omitempty"`
	ExtractedMetadata map[string]string `json:"extractedMetadata,omitempty"`
	Error             string            `json:"error,omitempty"`
}

// Validate implements custom validation logic for FirmwareBundle
func (r *FirmwareBundle) Validate(ctx context.Context) error {
	if err := ValidateRegistryURLFormat(r.Spec.RegistryURL); err != nil {
		return err
	}
	if err := ValidateRepositoryFormat(r.Spec.Repository); err != nil {
		return err
	}
	if err := ValidateTagOrDigestFormat(r.Spec.TagOrDigest); err != nil {
		return err
	}
	return nil
}

func ValidateRegistryURLFormat(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("spec.registryURL is required")
	}
	if strings.Contains(trimmed, "://") {
		return fmt.Errorf("spec.registryURL must not include a URL scheme")
	}
	if strings.Contains(trimmed, "/") {
		return fmt.Errorf("spec.registryURL must not include path segments")
	}
	if strings.ContainsAny(trimmed, " \t\n\r") {
		return fmt.Errorf("spec.registryURL must not contain whitespace")
	}
	if strings.Trim(trimmed, ".") == "" {
		return fmt.Errorf("spec.registryURL is invalid")
	}
	return nil
}

func ValidateRepositoryFormat(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("spec.repository is required")
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasSuffix(trimmed, "/") {
		return fmt.Errorf("spec.repository must not start or end with '/'")
	}
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if !firmwareRepositorySegmentPattern.MatchString(part) {
			return fmt.Errorf("spec.repository contains invalid segment %q", part)
		}
	}
	return nil
}

func ValidateTagOrDigestFormat(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("spec.tagOrDigest is required")
	}
	if firmwareDigestPattern.MatchString(trimmed) || firmwareTagPattern.MatchString(trimmed) {
		return nil
	}
	return fmt.Errorf("spec.tagOrDigest must be a valid OCI tag or sha256 digest")
}

// GetKind returns the kind of the resource
func (r *FirmwareBundle) GetKind() string {
	return "FirmwareBundle"
}

// GetName returns the name of the resource
func (r *FirmwareBundle) GetName() string {
	return r.Metadata.Name
}

// GetUID returns the UID of the resource
func (r *FirmwareBundle) GetUID() string {
	return r.Metadata.UID
}

// IsHub marks this as the hub/storage version
func (r *FirmwareBundle) IsHub() {}
