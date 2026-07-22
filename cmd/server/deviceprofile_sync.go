// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/user/firmware-updater/pkg/deviceProfiles"
)

// loadDeviceProfiles scans dir for profile YAML files and populates the global
// registry. Load errors are logged but non-fatal so a single malformed profile
// does not prevent the server from starting.
func loadDeviceProfiles(dir string) {
	log.Printf("Loading device profiles from %s", dir)

	errs := deviceProfiles.LoadDirectory(dir, deviceProfiles.Global)
	for _, err := range errs {
		log.Printf("  device profile load warning: %v", err)
	}

	loaded := len(deviceProfiles.Global.List())
	log.Printf("Loaded %d device profile(s) (%d warning(s))", loaded, len(errs))
}
