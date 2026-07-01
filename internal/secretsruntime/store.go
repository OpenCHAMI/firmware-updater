package secretsruntime

import (
	"fmt"
	"sync"

	"github.com/OpenCHAMI/magellan/pkg/secrets"
)

var (
	mu    sync.RWMutex
	store secrets.SecretStore
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
