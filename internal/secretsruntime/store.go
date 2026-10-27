package secretsruntime

import (
	"fmt"
	"sync"

	"github.com/OpenCHAMI/magellan/pkg/secrets"
)

var (
	mu          sync.RWMutex
	reloadMu    sync.Mutex
	store       secrets.SecretStore
	storeLoader func() (secrets.SecretStore, error)
)

// SetStore registers the process-wide secret store exactly once.
func SetStore(s secrets.SecretStore) error {
	if s == nil {
		return fmt.Errorf("secret store is nil")
	}

	mu.Lock()
	defer mu.Unlock()

	if store != nil {
		return fmt.Errorf("secret store already initialized")
	}

	store = s
	return nil
}

// GetStore returns the process-wide secret store.
func GetStore() secrets.SecretStore {
	mu.RLock()
	defer mu.RUnlock()
	return store
}

// SetStoreLoader configures how the process-wide store is refreshed from its
// backing source. It is normally set during server initialization.
func SetStoreLoader(loader func() (secrets.SecretStore, error)) error {
	if loader == nil {
		return fmt.Errorf("secret store loader is nil")
	}

	mu.Lock()
	defer mu.Unlock()
	storeLoader = loader
	return nil
}

// ReloadStore replaces the process-wide store with a fresh copy from its
// configured loader. Stores without a loader, such as test fakes, are left as-is.
func ReloadStore() error {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	mu.RLock()
	loader := storeLoader
	mu.RUnlock()
	if loader == nil {
		return nil
	}

	loadedStore, err := loader()
	if err != nil {
		return fmt.Errorf("load secret store: %w", err)
	}
	if loadedStore == nil {
		return fmt.Errorf("secret store loader returned nil")
	}

	mu.Lock()
	store = loadedStore
	mu.Unlock()
	return nil
}
