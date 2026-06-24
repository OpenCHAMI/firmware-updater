package firmwareproxy

import (
	"context"
	"testing"
)

func TestResolverCredentialForHostMatch(t *testing.T) {
	resolver := NewResolver("quay.io", "robot$user", "$super-secret")

	cred, err := resolver.credentialForHost(context.TODO(), "quay.io")
	if err != nil {
		t.Fatalf("credentialForHost returned error: %v", err)
	}

	if cred.Username != "robot$user" {
		t.Fatalf("expected username to be preserved, got %q", cred.Username)
	}
	if cred.Password != "$super-secret" {
		t.Fatalf("expected password to be preserved, got %q", cred.Password)
	}
}

func TestResolverCredentialForHostNoMatch(t *testing.T) {
	resolver := NewResolver("quay.io", "robot$user", "$super-secret")

	cred, err := resolver.credentialForHost(context.TODO(), "registry-1.docker.io")
	if err != nil {
		t.Fatalf("credentialForHost returned error: %v", err)
	}

	if cred.Username != "" || cred.Password != "" {
		t.Fatalf("expected empty credential for non-matching host, got username=%q", cred.Username)
	}
}

func TestSelectManifestCandidateLatest(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "v1", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111"},
		{tag: "v2", versionRaw: "1.10.0", versionNormalized: "v1.10.0", payloadDigest: "sha256:222"},
		{tag: "v3", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:333"},
	}

	selected, err := selectManifestCandidate(candidates, "latest")
	if err != nil {
		t.Fatalf("selectManifestCandidate returned error: %v", err)
	}

	if selected.versionNormalized != "v1.10.0" {
		t.Fatalf("expected highest version v1.10.0, got %s", selected.versionNormalized)
	}
	if selected.payloadDigest != "sha256:222" {
		t.Fatalf("expected digest sha256:222, got %s", selected.payloadDigest)
	}
}

func TestSelectManifestCandidateExactVersion(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222"},
	}

	selected, err := selectManifestCandidate(candidates, "1.2.0")
	if err != nil {
		t.Fatalf("selectManifestCandidate returned error: %v", err)
	}

	if selected.tag != "tag-a" {
		t.Fatalf("expected tag-a, got %s", selected.tag)
	}
}

func TestSelectManifestCandidateInvalidTarget(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111"},
	}

	_, err := selectManifestCandidate(candidates, "not-semver")
	if err == nil {
		t.Fatalf("expected error for invalid version target")
	}
}

func TestIsCompatibleHardware(t *testing.T) {
	annotation := "x1000, x2000; x3000"

	if !isCompatibleHardware(annotation, "x2000") {
		t.Fatalf("expected x2000 to be compatible")
	}
	if isCompatibleHardware(annotation, "x9999") {
		t.Fatalf("did not expect x9999 to be compatible")
	}
}
