package semverutil

import (
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

var (
	semverTokenPattern     = regexp.MustCompile(`v?\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)
	semverCandidatePattern = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))?((?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)
)

// NormalizeSemverCandidate normalizes a direct version string into canonical semver.
// It accepts MAJOR.MINOR and pads missing patch as MAJOR.MINOR.0.
func NormalizeSemverCandidate(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}

	candidate := trimmed
	if !strings.HasPrefix(candidate, "v") {
		candidate = "v" + candidate
	}
	if semver.IsValid(candidate) {
		return semver.Canonical(candidate), true
	}

	parts := semverCandidatePattern.FindStringSubmatch(trimmed)
	if len(parts) != 5 {
		return "", false
	}

	patch := parts[3]
	if patch == "" {
		patch = "0"
	}

	padded := "v" + parts[1] + "." + parts[2] + "." + patch + parts[4]
	if !semver.IsValid(padded) {
		return "", false
	}

	return semver.Canonical(padded), true
}

// NormalizeComparableSemver normalizes a full string or an embedded semver-like token.
func NormalizeComparableSemver(raw string) (string, bool) {
	if normalized, ok := NormalizeSemverCandidate(raw); ok {
		return normalized, true
	}

	token := semverTokenPattern.FindString(strings.TrimSpace(raw))
	if token == "" {
		return "", false
	}

	return NormalizeSemverCandidate(token)
}
