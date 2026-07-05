package firmwareproxy

import (
	"testing"

	"oras.land/oras-go/v2/registry/remote"
)

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

func TestSelectNewerManifestCandidate(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222"},
	}

	selected, updateAvailable, err := selectNewerManifestCandidate(candidates, "nc.1.2.0-build42")
	if err != nil {
		t.Fatalf("selectNewerManifestCandidate returned error: %v", err)
	}
	if !updateAvailable {
		t.Fatalf("expected update to be available")
	}
	if selected.tag != "tag-b" {
		t.Fatalf("expected tag-b, got %s", selected.tag)
	}
}

func TestSelectNewerManifestCandidateNoUpdateNeeded(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222"},
	}

	_, updateAvailable, err := selectNewerManifestCandidate(candidates, "1.3.0")
	if err != nil {
		t.Fatalf("selectNewerManifestCandidate returned error: %v", err)
	}
	if updateAvailable {
		t.Fatalf("expected no update to be available")
	}
}

func TestIsCompatibleHardwareAny(t *testing.T) {
	annotation := "x1000, x2000; x3000"

	if !isCompatibleHardwareAny(annotation, []string{"foo", "x2000"}) {
		t.Fatalf("expected x2000 to match compatibility annotation")
	}
	if isCompatibleHardwareAny(annotation, []string{"foo", "bar"}) {
		t.Fatalf("did not expect non-matching hints to match compatibility annotation")
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

func TestApplyRepoAuthConfigured(t *testing.T) {
	InitAuth("test-user", "test-pass")
	t.Cleanup(func() { InitAuth("", "") })

	repo, err := remote.NewRepository("example.com/fw/repo")
	if err != nil {
		t.Fatalf("remote.NewRepository returned error: %v", err)
	}

	applyRepoAuth(repo)
	if repo.Client == nil {
		t.Fatalf("expected repo client to be configured with auth")
	}
}

func TestApplyRepoAuthMissingCredentials(t *testing.T) {
	InitAuth("", "")

	repo, err := remote.NewRepository("example.com/fw/repo")
	if err != nil {
		t.Fatalf("remote.NewRepository returned error: %v", err)
	}

	applyRepoAuth(repo)
	if repo.Client != nil {
		t.Fatalf("expected repo client to remain nil when credentials are missing")
	}
}
