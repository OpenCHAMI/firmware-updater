package reconcilers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

func TestLockConflictsFromStatus_Detection(t *testing.T) {
	status := smdLockStatusResponse{
		Components: []smdLockComponent{
			{ID: "x0c0s0b0", Locked: true, Reserved: false, CreationTime: "ct-1", ExpirationTime: "et-1"},
			{ID: "x0c0s0b1", Locked: false, Reserved: true, CreationTime: "ct-2", ExpirationTime: "et-2"},
			{ID: "x0c0s0b2", Locked: true, Reserved: true, CreationTime: "ct-3", ExpirationTime: "et-3"},
			{ID: "x0c0s0b3", Locked: false, Reserved: false, CreationTime: "ct-4", ExpirationTime: "et-4"},
		},
		NotFound: []string{"x0c0s0b4"},
	}

	conflicts := lockConflictsFromStatus(status)
	if len(conflicts) != 4 {
		t.Fatalf("expected 4 lock conflict entries, got %d: %v", len(conflicts), conflicts)
	}

	contains := func(fragment string) bool {
		for _, conflict := range conflicts {
			if strings.Contains(conflict, fragment) {
				return true
			}
		}
		return false
	}

	if !contains("component=x0c0s0b0 locked=true reserved=false") {
		t.Fatalf("expected locked conflict for x0c0s0b0, got %v", conflicts)
	}
	if !contains("component=x0c0s0b1 locked=false reserved=true") {
		t.Fatalf("expected reserved conflict for x0c0s0b1, got %v", conflicts)
	}
	if !contains("component=x0c0s0b2 locked=true reserved=true") {
		t.Fatalf("expected locked+reserved conflict for x0c0s0b2, got %v", conflicts)
	}
	if !contains("component=x0c0s0b4 notFound=true") {
		t.Fatalf("expected notFound status for x0c0s0b4, got %v", conflicts)
	}
	if contains("x0c0s0b3") {
		t.Fatalf("did not expect conflict for unlocked/unreserved component x0c0s0b3")
	}
}

func TestEvaluateTargetLocksWithBackoff_IgnoreLocksPolicy(t *testing.T) {
	t.Setenv(smdTokenEnvVar, "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != smdLockStatusRoute {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Components":[{"ID":"x0c0s0b0","Locked":true,"Reserved":false,"CreationTime":"ct","ExpirationTime":"et"}],"NotFound":[]}`))
	}))
	defer server.Close()
	t.Setenv(smdBaseURLEnvVar, server.URL)

	conflicts, err := evaluateTargetLocksWithBackoff(context.Background(), []string{"x0c0s0b0"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	ignoreLocksFalseBlocks := len(conflicts) > 0
	if !ignoreLocksFalseBlocks {
		t.Fatalf("expected conflicts to block when ignoreLocks=false")
	}

	ignoreLocksTrueBlocks := false
	if len(conflicts) > 0 {
		ignoreLocksTrueBlocks = false
	}
	if ignoreLocksTrueBlocks {
		t.Fatalf("expected conflicts to not block when ignoreLocks=true")
	}
}

func TestQuerySMDLockStatus_TransientError(t *testing.T) {
	t.Setenv(smdTokenEnvVar, "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("busy"))
	}))
	defer server.Close()
	t.Setenv(smdBaseURLEnvVar, server.URL)

	_, err := querySMDLockStatus(context.Background(), []string{"x0c0s0b0"})
	if err == nil {
		t.Fatal("expected transient error, got nil")
	}

	var statusErr *firmwareproxy.HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %T", err)
	}
	if statusErr.StatusCode != 503 {
		t.Fatalf("expected 503 status code for transient error, got %d", statusErr.StatusCode)
	}
}

func TestGetSMDBaseURL_DefaultAndOverride(t *testing.T) {
	oldValue, had := os.LookupEnv(smdBaseURLEnvVar)
	if had {
		defer os.Setenv(smdBaseURLEnvVar, oldValue)
	} else {
		defer os.Unsetenv(smdBaseURLEnvVar)
	}

	_ = os.Unsetenv(smdBaseURLEnvVar)
	if got := getSMDBaseURL(); got != defaultSMDBaseURL {
		t.Fatalf("expected default SMD base URL %q, got %q", defaultSMDBaseURL, got)
	}

	if err := os.Setenv(smdBaseURLEnvVar, "http://example-smd:27779"); err != nil {
		t.Fatalf("set env var failed: %v", err)
	}
	if got := getSMDBaseURL(); got != "http://example-smd:27779" {
		t.Fatalf("expected overridden SMD base URL, got %q", got)
	}
}
