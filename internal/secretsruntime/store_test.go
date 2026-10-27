package secretsruntime

import (
	"errors"
	"testing"

	"github.com/OpenCHAMI/magellan/pkg/secrets"
)

type testSecretStore map[string]string

func (s testSecretStore) GetSecretByID(secretID string) (string, error) {
	return s[secretID], nil
}

func (s testSecretStore) StoreSecretByID(secretID, secret string) error {
	s[secretID] = secret
	return nil
}

func (s testSecretStore) ListSecrets() (map[string]string, error) {
	return map[string]string(s), nil
}

func (s testSecretStore) RemoveSecretByID(secretID string) error {
	delete(s, secretID)
	return nil
}

func TestReloadStoreReplacesStoreAndPreservesItOnLoadError(t *testing.T) {
	initial := testSecretStore{"initial": "value"}
	if err := SetStore(initial); err != nil {
		t.Fatalf("SetStore() error = %v", err)
	}

	refreshed := testSecretStore{"new": "credential"}
	if err := SetStoreLoader(func() (secrets.SecretStore, error) {
		return refreshed, nil
	}); err != nil {
		t.Fatalf("SetStoreLoader() error = %v", err)
	}

	if err := ReloadStore(); err != nil {
		t.Fatalf("ReloadStore() error = %v", err)
	}
	if got := GetStore(); got != nil {
		value, err := got.GetSecretByID("new")
		if err != nil || value != "credential" {
			t.Fatalf("reloaded store returned (%q, %v), want credential", value, err)
		}
	} else {
		t.Fatal("GetStore() = nil after reload")
	}

	loadErr := errors.New("secrets file unavailable")
	if err := SetStoreLoader(func() (secrets.SecretStore, error) {
		return nil, loadErr
	}); err != nil {
		t.Fatalf("SetStoreLoader() error = %v", err)
	}
	if err := ReloadStore(); !errors.Is(err, loadErr) {
		t.Fatalf("ReloadStore() error = %v, want %v", err, loadErr)
	}
	value, err := GetStore().GetSecretByID("new")
	if err != nil || value != "credential" {
		t.Fatalf("store after failed reload returned (%q, %v), want credential", value, err)
	}
}
