// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package main

import (
	"github.com/go-chi/chi/v5"
)

// RegisterCampaignJobAliasRoutes registers short-name aliases for the
// FirmwareUpdateCampaign and FirmwareUpdateJob resource routes:
//   - /campaigns -> same handlers as /firmwareupdatecampaigns
//   - /jobs      -> same handlers as /firmwareupdatejobs
//
// These are hand-written aliases, not Fabrica-generated. They must be kept
// in sync manually with routes_generated.go if that file's route shape
// changes (e.g. new subresources). Call this after RegisterGeneratedRoutes
// in main.go.
func RegisterCampaignJobAliasRoutes(r chi.Router) {
	r.Group(func(protected chi.Router) {
		protected.Route("/campaigns", func(r chi.Router) {
			r.Get("/", GetFirmwareUpdateCampaigns)
			r.Post("/", CreateFirmwareUpdateCampaign)
			r.Route("/{uid}", func(r chi.Router) {
				r.Get("/", GetFirmwareUpdateCampaign)
				r.Put("/", UpdateFirmwareUpdateCampaign)
				r.Patch("/", PatchFirmwareUpdateCampaign)
				r.Delete("/", DeleteFirmwareUpdateCampaign)

				r.Route("/status", func(r chi.Router) {
					r.Put("/", UpdateFirmwareUpdateCampaignStatus)
					r.Patch("/", PatchFirmwareUpdateCampaignStatus)
				})
			})
		})

		protected.Route("/jobs", func(r chi.Router) {
			r.Get("/", GetFirmwareUpdateJobs)
			r.Post("/", CreateFirmwareUpdateJob)
			r.Route("/{uid}", func(r chi.Router) {
				r.Get("/", GetFirmwareUpdateJob)
				r.Put("/", UpdateFirmwareUpdateJob)
				r.Patch("/", PatchFirmwareUpdateJob)
				r.Delete("/", DeleteFirmwareUpdateJob)

				r.Route("/status", func(r chi.Router) {
					r.Put("/", UpdateFirmwareUpdateJobStatus)
					r.Patch("/", PatchFirmwareUpdateJobStatus)
				})
			})
		})
	})
}
