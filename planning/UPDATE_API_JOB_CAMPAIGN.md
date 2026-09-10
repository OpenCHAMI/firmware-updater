# Plan: Add `/campaigns` and `/jobs` API aliases

## Goal

Expose the existing `FirmwareUpdateCampaign` and `FirmwareUpdateJob` resources
under shorter route prefixes:

- `/firmwareupdatecampaigns` → also available as `/campaigns`
- `/firmwareupdatejobs` → also available as `/jobs`

The original `/firmwareupdatecampaigns` and `/firmwareupdatejobs` routes,
resource prefixes, CLI commands, and all Fabrica-generated code must continue
to work unchanged. Nothing in `pkg/codegen/templates/*.tmpl`, `apis.yaml`, or
any `*_generated.go` file should be edited, since those are owned by
`fabrica generate` and would be overwritten (or cause drift) on the next
regeneration.

## Background

- Routes are registered by the generated `RegisterGeneratedRoutes` in
  [cmd/server/routes_generated.go](../cmd/server/routes_generated.go), which
  registers `/firmwareupdatecampaigns` and `/firmwareupdatejobs` and wires
  them to generated handlers (`GetFirmwareUpdateCampaigns`,
  `CreateFirmwareUpdateCampaign`, etc.) in
  `cmd/server/firmwareupdatecampaign_handlers_generated.go` and
  `cmd/server/firmwareupdatejob_handlers_generated.go`.
- The generated file itself documents the extension point:
  > To add custom routes:
  > 1. Create a separate `RegisterCustomRoutes` function
  > 2. Call it after `RegisterGeneratedRoutes` in main.go
- This pattern is already used in the repo for `deviceprofiles`: see
  [cmd/server/deviceprofile_routes.go](../cmd/server/deviceprofile_routes.go),
  registered via `RegisterDeviceProfileRoutes(r)` in
  [cmd/server/main.go](../cmd/server/main.go) right after
  `RegisterGeneratedRoutes(r)`.
- Because the generated handlers are plain functions in `package main`, a new
  file in `cmd/server` can call them directly — no duplication of business
  logic, storage access, or validation is needed.

## Approach

Add a new **hand-written, non-generated** file that registers alias routes
pointing at the existing generated handlers, and call it from `main.go` next
to the other custom route registrations. No generated file is touched.

### 1. New file: `cmd/server/campaign_job_aliases.go`

```go
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
```

This mirrors the exact route shape in `routes_generated.go`, just under a
shorter path, and reuses the same generated handler functions — so behavior
(validation, storage, status codes, response bodies) is identical.

### 2. Wire it up in `cmd/server/main.go`

Add one line after the existing custom route registrations:

```go
RegisterGeneratedRoutes(r)
registerFirmwareProxyRoute(r)
RegisterDeviceProfileRoutes(r)
RegisterCampaignJobAliasRoutes(r)   // new: /campaigns, /jobs aliases
r.Get("/health", healthHandler)
```

### 3. Resource UID prefixes — leave untouched

`registerResourcePrefixes()` in `routes_generated.go` registers
`"firmwareupdatecampaign"` / `"firmwareupdatejob"` as UID prefixes. These are
identity concerns (used in stored UIDs like `firmwareupdatecampaign-abc123`)
and are independent of the URL path used to reach the resource. Do **not**
add or change prefixes — existing UIDs and any code parsing them must keep
working, and route aliasing doesn't require it.

### 4. OpenAPI / docs surface (optional, follow-up)

The generated OpenAPI spec (served via `/openapi.json`, see
`cmd/server/openapi_extensions.go`) is built from the generated routes and
will only describe `/firmwareupdatecampaigns` and `/firmwareupdatejobs`
unless we add documentation for the aliases too. Options, in order of
preference:
- Leave the generated OpenAPI doc as the canonical reference and mention the
  aliases in prose in `README.md` / `docs/user-guide.md`.
- If full OpenAPI coverage of the aliases is wanted later, extend
  `openapi_extensions.go` (already a hand-written file with a `register*`
  pattern) to add matching path entries — not required for the aliases to
  function.

This plan does not require changes here to satisfy the core request; listed
for completeness.

### 5. Documentation updates

Update examples (not behavior) in:
- [README.md](../README.md)
- [docs/user-guide.md](../docs/user-guide.md)

Add a short note that `/campaigns` and `/jobs` are equivalent short-name
aliases for `/firmwareupdatecampaigns` and `/firmwareupdatejobs`, keeping the
existing long-form examples as-is (both continue to work).

### 6. CLI client (optional, out of scope unless requested)

`cmd/client/main.go` and `pkg/client/client_generated.go` are Fabrica
generated and expose `firmwareupdatecampaign` / `firmwareupdatejob`
subcommands and Go client methods. These call the long-form REST paths
directly and are unaffected by the new server-side aliases — no change
needed for the HTTP-level alias to work. Adding short CLI aliases (e.g. a
`campaign`/`job` subcommand alias) would be a separate, purely additive
change in a new hand-written file, not in the generated client — skip unless
explicitly requested.

## What is explicitly NOT changed

- `apis.yaml`
- `apis/hardware.fabrica.dev/v1/firmwareupdatecampaign_types.go`,
  `firmwareupdatejob_types.go` (and their tests)
- `cmd/server/routes_generated.go`
- `cmd/server/firmwareupdatecampaign_handlers_generated.go`
- `cmd/server/firmwareupdatejob_handlers_generated.go`
- `pkg/resources/register_generated.go`
- `pkg/reconcilers/*_generated.go` and hand-written reconcilers
  (`firmwareupdatecampaign_reconciler.go`, `firmwareupdatejob_reconciler.go`)
- `pkg/client/client_generated.go`, `cmd/client/main.go`
- `internal/storage/*_generated.go`

All Kind names, JSON field names, storage schema, and existing
`/firmwareupdatecampaigns` and `/firmwareupdatejobs` routes remain identical.
The new `/campaigns` and `/jobs` paths are purely additive HTTP route
aliases implemented in one new file plus a one-line registration call.

## Implementation steps checklist

1. [x] Create `cmd/server/campaign_job_aliases.go` with
       `RegisterCampaignJobAliasRoutes`.
2. [x] Call `RegisterCampaignJobAliasRoutes(r)` in `cmd/server/main.go` after
       `RegisterGeneratedRoutes(r)`.
3. [x] Run `go build ./...` and existing tests to confirm nothing broke.
4. [ ] Manually verify (or add a small test) that `GET /campaigns` and
       `GET /jobs` return the same data as `GET /firmwareupdatecampaigns`
       and `GET /firmwareupdatejobs`.
5. [x] Update `README.md` and `docs/user-guide.md` with a short mention of
       the new aliases.
6. [ ] (Optional) Decide whether to extend OpenAPI docs / CLI with the short
       names — not required for the core request.
7. [x] Create a document describing the changes named HANDOFF-UPDATE_API.md
