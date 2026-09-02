package firmwareproxy

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

func TestSelectManifestCandidateLatest(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "v1", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111", payloadFilename: "fw-a.bin"},
		{tag: "v2", versionRaw: "1.10.0", versionNormalized: "v1.10.0", payloadDigest: "sha256:222", payloadFilename: "fw-b.bin"},
		{tag: "v3", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:333", payloadFilename: "fw-c.bin"},
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
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111", payloadFilename: "fw-a.bin"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222", payloadFilename: "fw-b.bin"},
	}

	selected, err := selectManifestCandidate(candidates, "1.2.0")
	if err != nil {
		t.Fatalf("selectManifestCandidate returned error: %v", err)
	}

	if selected.tag != "tag-a" {
		t.Fatalf("expected tag-a, got %s", selected.tag)
	}
}

func TestSelectManifestCandidateExactTwoComponentVersion(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111", payloadFilename: "fw-a.bin"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222", payloadFilename: "fw-b.bin"},
	}

	selected, err := selectManifestCandidate(candidates, "1.2")
	if err != nil {
		t.Fatalf("selectManifestCandidate returned error: %v", err)
	}

	if selected.tag != "tag-a" {
		t.Fatalf("expected tag-a, got %s", selected.tag)
	}
}

func TestSelectManifestCandidateInvalidTarget(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111", payloadFilename: "fw-a.bin"},
	}

	_, err := selectManifestCandidate(candidates, "not-semver")
	if err == nil {
		t.Fatalf("expected error for invalid version target")
	}
}

func TestSelectNewerManifestCandidate(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111", payloadFilename: "fw-a.bin"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222", payloadFilename: "fw-b.bin"},
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
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111", payloadFilename: "fw-a.bin"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222", payloadFilename: "fw-b.bin"},
	}

	selected, updateAvailable, err := selectNewerManifestCandidate(candidates, "1.3.0")
	if err != nil {
		t.Fatalf("selectNewerManifestCandidate returned error: %v", err)
	}
	if updateAvailable {
		t.Fatalf("expected no update to be available")
	}
	if selected.tag != "tag-b" {
		t.Fatalf("expected current latest tag-b, got %s", selected.tag)
	}
}

func TestVersionsMatch(t *testing.T) {
	if !VersionsMatch("1.3.0", " 1.3.0 ") {
		t.Fatal("expected matching version strings to match")
	}
	if VersionsMatch("1.3.0", "v1.3.0") {
		t.Fatal("did not expect different version strings to match")
	}
}

func TestSelectNewerManifestCandidateTwoComponentInstalledVersion(t *testing.T) {
	candidates := []manifestCandidate{
		{tag: "tag-a", versionRaw: "1.2.0", versionNormalized: "v1.2.0", payloadDigest: "sha256:111", payloadFilename: "fw-a.bin"},
		{tag: "tag-b", versionRaw: "1.3.0", versionNormalized: "v1.3.0", payloadDigest: "sha256:222", payloadFilename: "fw-b.bin"},
	}

	selected, updateAvailable, err := selectNewerManifestCandidate(candidates, "1.2")
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

func TestIsCompatibleHardwareAny(t *testing.T) {
	annotation := "x1000, x2000; x3000"

	if !isCompatibleHardwareAny(annotation, []string{"foo", "x2000"}) {
		t.Fatalf("expected x2000 to match compatibility annotation")
	}
	if isCompatibleHardwareAny(annotation, []string{"foo", "bar"}) {
		t.Fatalf("did not expect non-matching hints to match compatibility annotation")
	}
}

func TestBuildManifestCandidateForInventoryMatchesTargetAndModel(t *testing.T) {
	manifest := ocispec.Manifest{
		ArtifactType: FirmwareBundleArtifactType,
		Annotations: map[string]string{
			annotationCompatibleHardware:       "BMC",
			"dev.fabrica.firmware.models":      "ProLiant DL385",
			"dev.fabrica.firmware.softwareIds": "ilo5",
			annotationFirmwareVersionString:    "1.2.3 build 7",
			annotationImageVersion:             "1.2.3",
		},
		Layers: []ocispec.Descriptor{{Digest: "sha256:111"}},
	}

	candidate, ok := buildManifestCandidateForInventory(manifest, "tag-a", inventoryIdentity{targets: []string{"BMC"}, model: "ProLiant DL385"})
	if !ok {
		t.Fatal("expected target and model to match")
	}
	if candidate.versionString != "1.2.3 build 7" {
		t.Fatalf("expected repository version string, got %q", candidate.versionString)
	}
}

func TestBuildManifestCandidateForInventoryMatchesTargetName(t *testing.T) {
	manifest := ocispec.Manifest{
		ArtifactType: FirmwareBundleArtifactType,
		Annotations: map[string]string{
			annotationCompatibleHardware: "Baseboard Management Controller",
			annotationFirmwareModels:     "ProLiant DL385",
			annotationImageVersion:       "1.2.3",
		},
		Layers: []ocispec.Descriptor{{Digest: "sha256:111"}},
	}

	_, ok := buildManifestCandidateForInventory(manifest, "tag-a", inventoryIdentity{targets: []string{"BMC", "Baseboard Management Controller"}, model: "ProLiant DL385"})
	if !ok {
		t.Fatal("expected the Redfish Name to match the registry target")
	}
}

func TestBuildManifestCandidateForInventorySoftwareIDTakesPrecedence(t *testing.T) {
	manifest := ocispec.Manifest{
		ArtifactType: FirmwareBundleArtifactType,
		Annotations: map[string]string{
			annotationCompatibleHardware:       "BMC",
			"dev.fabrica.firmware.models":      "ProLiant DL385",
			"dev.fabrica.firmware.softwareIds": "ilo5",
			annotationImageVersion:             "1.2.3",
		},
		Layers: []ocispec.Descriptor{{Digest: "sha256:111"}},
	}

	_, ok := buildManifestCandidateForInventory(manifest, "tag-a", inventoryIdentity{targets: []string{"BMC"}, model: "ProLiant DL385", softwareID: "ilo6"})
	if ok {
		t.Fatal("did not expect a model match to override a non-matching software ID")
	}
}

func TestInventoryCandidatesSelectLatestMatchingVersion(t *testing.T) {
	identity := inventoryIdentity{targets: []string{"BMC"}, model: "ProLiant DL385"}
	versions := []string{"1.2.0", "1.10.0", "1.3.0"}
	candidates := make([]manifestCandidate, 0, len(versions))
	for _, version := range versions {
		manifest := ocispec.Manifest{
			ArtifactType: FirmwareBundleArtifactType,
			Annotations: map[string]string{
				annotationCompatibleHardware: "BMC",
				annotationFirmwareModels:     "ProLiant DL385",
				annotationImageVersion:       version,
			},
			Layers: []ocispec.Descriptor{{Digest: "sha256:111"}},
		}
		candidate, ok := buildManifestCandidateForInventory(manifest, "tag-"+version, identity)
		if !ok {
			t.Fatalf("expected version %q to match inventory identity", version)
		}
		candidates = append(candidates, candidate)
	}

	selected, err := selectManifestCandidate(candidates, "latest")
	if err != nil {
		t.Fatalf("selectManifestCandidate returned error: %v", err)
	}
	if selected.versionNormalized != "v1.10.0" {
		t.Fatalf("expected highest matching version v1.10.0, got %s", selected.versionNormalized)
	}
}

func TestBuildManifestCandidateExtractsPayloadFilename(t *testing.T) {
	manifest := ocispec.Manifest{
		ArtifactType: FirmwareBundleArtifactType,
		Annotations: map[string]string{
			annotationCompatibleHardware:    "x1000",
			annotationImageVersion:          "1.2.3",
			annotationFirmwareVersionString: "1.2.3 build 7",
		},
		Layers: []ocispec.Descriptor{{
			Digest: "sha256:111",
			Annotations: map[string]string{
				annotationImageTitle: "dummy-video.bin",
			},
		}},
	}

	candidate, ok := buildManifestCandidate(manifest, "tag-a", "x1000")
	if !ok {
		t.Fatal("expected candidate to be selected")
	}
	if candidate.payloadFilename != "dummy-video.bin" {
		t.Fatalf("expected payload filename dummy-video.bin, got %q", candidate.payloadFilename)
	}
	if candidate.versionString != "1.2.3 build 7" {
		t.Fatalf("expected repository version string, got %q", candidate.versionString)
	}
}

func TestBuildManifestCandidateMissingPayloadFilenameDefaultsEmpty(t *testing.T) {
	manifest := ocispec.Manifest{
		ArtifactType: FirmwareBundleArtifactType,
		Annotations: map[string]string{
			annotationCompatibleHardware: "x1000",
			annotationImageVersion:       "1.2.3",
		},
		Layers: []ocispec.Descriptor{{
			Digest: "sha256:111",
		}},
	}

	candidate, ok := buildManifestCandidate(manifest, "tag-a", "x1000")
	if !ok {
		t.Fatal("expected candidate to be selected")
	}
	if candidate.payloadFilename != "" {
		t.Fatalf("expected empty payload filename, got %q", candidate.payloadFilename)
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
	t.Setenv(envRepositoryInsecureTLS, "false")
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

func TestNewRepositoryUsesHTTPSByDefault(t *testing.T) {
	t.Setenv(envRepositoryInsecureTLS, "false")

	repo, err := NewRepository("127.0.0.1:5000/firmware/ilo")
	if err != nil {
		t.Fatalf("NewRepository returned error: %v", err)
	}
	if repo.PlainHTTP {
		t.Fatal("expected HTTPS transport when insecure mode is disabled")
	}
}

func TestNewRepositoryRetainsHTTPSWhenInsecureEnabled(t *testing.T) {
	t.Setenv(envRepositoryInsecureTLS, "true")

	repo, err := NewRepository("registry.example.com/firmware/ilo")
	if err != nil {
		t.Fatalf("NewRepository returned error: %v", err)
	}
	if repo.PlainHTTP {
		t.Fatal("expected HTTPS transport when insecure mode is enabled")
	}
}

func TestApplyRepoAuthMissingCredentials(t *testing.T) {
	t.Setenv(envRepositoryInsecureTLS, "false")
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

func TestApplyRepoAuthMissingCredentialsInsecureTLS(t *testing.T) {
	t.Setenv(envRepositoryInsecureTLS, "true")
	InitAuth("", "")

	repo, err := remote.NewRepository("example.com/fw/repo")
	if err != nil {
		t.Fatalf("remote.NewRepository returned error: %v", err)
	}

	applyRepoAuth(repo)
	if repo.Client == nil {
		t.Fatalf("expected repo client to be configured when insecure TLS is enabled")
	}
}

func TestRepositoryInsecureTLSDefaultAndInvalid(t *testing.T) {
	t.Setenv(envRepositoryInsecureTLS, "")
	if repositoryInsecureTLS() {
		t.Fatalf("expected secure default when env var is unset")
	}

	t.Setenv(envRepositoryInsecureTLS, "not-a-bool")
	if repositoryInsecureTLS() {
		t.Fatalf("expected invalid env values to fall back to secure mode")
	}
}

func TestRepositoryInsecureTLSTrueValues(t *testing.T) {
	trueValues := []string{"true", "1", "TRUE"}

	for _, value := range trueValues {
		t.Setenv(envRepositoryInsecureTLS, value)
		if !repositoryInsecureTLS() {
			t.Fatalf("expected %q to enable insecure TLS", value)
		}
	}
}
