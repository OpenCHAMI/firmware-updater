// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package deviceProfiles

import (
	"fmt"
	"sync"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
)

// Registry is a thread-safe in-memory store of loaded DeviceProfiles.
type Registry struct {
	mu       sync.RWMutex
	profiles map[string]v1.DeviceProfile // keyed by Spec.ProfileID
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{profiles: make(map[string]v1.DeviceProfile)}
}

// Global is the process-wide registry instance.
var Global = NewRegistry()

// Register adds a profile to the registry. It returns an error if a profile
// with the same ProfileID already exists.
func (r *Registry) Register(p v1.DeviceProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.profiles[p.Spec.ProfileID]; exists {
		return fmt.Errorf("device profile %q already registered", p.Spec.ProfileID)
	}
	r.profiles[p.Spec.ProfileID] = p
	return nil
}

// Get returns the profile with the given ID and true, or a zero value and
// false if no such profile exists.
func (r *Registry) Get(id string) (v1.DeviceProfile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.profiles[id]
	return p, ok
}

// List returns all profiles currently in the registry.
func (r *Registry) List() []v1.DeviceProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]v1.DeviceProfile, 0, len(r.profiles))
	for _, p := range r.profiles {
		out = append(out, p)
	}
	return out
}

// Delete removes the profile with the given ID. It returns true if a profile
// was removed, false if none existed.
func (r *Registry) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.profiles[id]; !exists {
		return false
	}
	delete(r.profiles, id)
	return true
}

// Upsert adds a new profile or replaces an existing one with the same ID.
func (r *Registry) Upsert(p v1.DeviceProfile) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.profiles[p.Spec.ProfileID] = p
}
