// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/pkg/deviceProfiles"
)

// deviceProfileListResponse is the payload returned by GET /deviceprofiles.
type deviceProfileListResponse struct {
	Items []v1.DeviceProfile `json:"items"`
	Count int                `json:"count"`
}

// reloadResponse is the payload returned by POST /deviceprofiles/reload.
type reloadResponse struct {
	Loaded int      `json:"loaded"`
	Errors []string `json:"errors"`
}

// RegisterDeviceProfileRoutes registers the /deviceprofiles CRUD and reload
// endpoints. Call this after RegisterGeneratedRoutes in main.go.
func RegisterDeviceProfileRoutes(r chi.Router) {
	r.Route("/deviceprofiles", func(r chi.Router) {
		r.Get("/", listDeviceProfilesHandler)
		r.Post("/", createDeviceProfileHandler)
		r.Post("/reload", reloadDeviceProfilesHandler)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", getDeviceProfileHandler)
			r.Put("/", putDeviceProfileHandler)
			r.Patch("/", patchDeviceProfileHandler)
			r.Delete("/", deleteDeviceProfileHandler)
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// listDeviceProfilesHandler handles GET /deviceprofiles.
func listDeviceProfilesHandler(w http.ResponseWriter, r *http.Request) {
	profiles := deviceProfiles.Global.List()
	writeJSON(w, http.StatusOK, deviceProfileListResponse{
		Items: profiles,
		Count: len(profiles),
	})
}

// getDeviceProfileHandler handles GET /deviceprofiles/{id}.
func getDeviceProfileHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	profile, ok := deviceProfiles.Global.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// createDeviceProfileHandler handles POST /deviceprofiles.
func createDeviceProfileHandler(w http.ResponseWriter, r *http.Request) {
	var profile v1.DeviceProfile
	if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	normalizeDeviceProfile(&profile)

	if err := profile.Validate(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := deviceProfiles.Global.Register(profile); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, profile)
}

// putDeviceProfileHandler handles PUT /deviceprofiles/{id} (full replace).
func putDeviceProfileHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var profile v1.DeviceProfile
	if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// The URL id is authoritative.
	profile.Spec.ProfileID = id
	normalizeDeviceProfile(&profile)

	if err := profile.Validate(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	deviceProfiles.Global.Upsert(profile)
	writeJSON(w, http.StatusOK, profile)
}

// patchDeviceProfileHandler handles PATCH /deviceprofiles/{id} (partial merge).
func patchDeviceProfileHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	existing, ok := deviceProfiles.Global.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}

	// Merge by decoding the patch on top of the existing spec.
	if err := json.NewDecoder(r.Body).Decode(&existing); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// The URL id is authoritative and cannot be changed via patch.
	existing.Spec.ProfileID = id
	normalizeDeviceProfile(&existing)

	if err := existing.Validate(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	deviceProfiles.Global.Upsert(existing)
	writeJSON(w, http.StatusOK, existing)
}

// deleteDeviceProfileHandler handles DELETE /deviceprofiles/{id}.
func deleteDeviceProfileHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !deviceProfiles.Global.Delete(id) {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reloadDeviceProfilesHandler handles POST /deviceprofiles/reload. It rescans
// the configured device-profiles directory and replaces registry contents.
func reloadDeviceProfilesHandler(w http.ResponseWriter, r *http.Request) {
	fresh := deviceProfiles.NewRegistry()
	loadErrs := deviceProfiles.LoadDirectory(config.DeviceProfilesDir, fresh)

	// Replace the global registry contents with the freshly loaded set.
	for _, p := range fresh.List() {
		deviceProfiles.Global.Upsert(p)
	}

	errStrings := make([]string, 0, len(loadErrs))
	for _, e := range loadErrs {
		errStrings = append(errStrings, e.Error())
	}

	writeJSON(w, http.StatusOK, reloadResponse{
		Loaded: len(fresh.List()),
		Errors: errStrings,
	})
}

// normalizeDeviceProfile fills in the Fabrica envelope defaults and metadata for
// a profile received over the API.
func normalizeDeviceProfile(p *v1.DeviceProfile) {
	if p.APIVersion == "" {
		p.APIVersion = "hardware.fabrica.dev/v1"
	}
	if p.Kind == "" {
		p.Kind = "DeviceProfile"
	}
	if p.Metadata.Name == "" {
		p.Metadata.Name = p.Spec.ProfileID
	}
	if p.Spec.UpdateMethod == "" {
		p.Spec.UpdateMethod = "POST"
	}
	if p.Spec.SupportsInventoryExpand && p.Spec.FirmwareInventoryExpandParam == "" {
		p.Spec.FirmwareInventoryExpandParam = "?$expand=."
	}
	p.Status.LoadedAt = time.Now().UTC().Format(time.RFC3339)
}
