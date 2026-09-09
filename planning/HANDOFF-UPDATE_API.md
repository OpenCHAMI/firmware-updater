# Handoff: `/campaigns` and `/jobs` API Aliases

Status: **Implemented**
Plan: [UPDATE_API_JOB_CAMPAIGN.md](UPDATE_API_JOB_CAMPAIGN.md)

## Summary

Added short-name HTTP route aliases for the two Fabrica-managed resources:

| Resource               | Original route              | New alias      |
|------------------------|------------------------------|-----------------|
| `FirmwareUpdateCampaign` | `/firmwareupdatecampaigns`  | `/campaigns`    |
| `FirmwareUpdateJob`      | `/firmwareupdatejobs`       | `/jobs`         |

Both forms are live simultaneously and call the exact same generated
handlers, so behavior (validation, storage, status codes, response bodies)
is identical regardless of which path is used. Nothing Fabrica-generated was
modified.

## Files changed

- **New:** [cmd/server/campaign_job_aliases.go](../cmd/server/campaign_job_aliases.go)
  — hand-written, non-generated file. Defines
  `RegisterCampaignJobAliasRoutes(r chi.Router)`, which registers `/campaigns`
  and `/jobs` (including `/{uid}` and `/{uid}/status` subroutes) pointing at
  the existing generated handler functions
  (`GetFirmwareUpdateCampaigns`, `CreateFirmwareUpdateCampaign`,
  `GetFirmwareUpdateJobs`, `CreateFirmwareUpdateJob`, etc.).
- **Modified:** [cmd/server/main.go](../cmd/server/main.go) — added one line
  calling `RegisterCampaignJobAliasRoutes(r)` right after
  `RegisterGeneratedRoutes(r)` / `RegisterDeviceProfileRoutes(r)`, following
  the same pattern used for the `deviceprofiles` custom routes.
- **Modified:** [README.md](../README.md) — added a short callout note near
  the top that `/campaigns` and `/jobs` are equivalent aliases.
- **Modified:** [docs/user-guide.md](../docs/user-guide.md) — same callout
  note added to the Overview section.

## What was intentionally NOT changed

- `apis.yaml`
- `apis/hardware.fabrica.dev/v1/firmwareupdatecampaign_types.go` /
  `firmwareupdatejob_types.go`
- `cmd/server/routes_generated.go`
- `cmd/server/firmwareupdatecampaign_handlers_generated.go` /
  `firmwareupdatejob_handlers_generated.go`
- `pkg/resources/register_generated.go`
- `pkg/reconcilers/*` (generated and hand-written reconcilers)
- `pkg/client/client_generated.go`, `cmd/client/main.go`
- `internal/storage/*_generated.go`
- Resource UID prefixes (`firmwareupdatecampaign-...`, `firmwareupdatejob-...`)
  registered in `registerResourcePrefixes()` — these remain the identity
  prefix for stored UIDs regardless of which URL path was used to create the
  resource.

All existing `/firmwareupdatecampaigns` and `/firmwareupdatejobs` routes,
Kind names, JSON field names, and storage schema are unaffected.

## Verification performed

- `go build ./...` — succeeds with no errors.
- `get_errors` on the two changed Go files — no diagnostics.

## Not done / follow-ups (optional, out of scope unless requested)

1. **OpenAPI spec** (`/openapi.json`, built in
   `cmd/server/openapi_extensions.go`) currently only documents the long-form
   paths. Adding matching `/campaigns` / `/jobs` entries there would give full
   OpenAPI coverage of the aliases but isn't required for them to function.
2. **CLI aliases**: `cmd/client/main.go` / `pkg/client/client_generated.go`
   still only expose `firmwareupdatecampaign` / `firmwareupdatejob`
   subcommands and call the long-form REST paths. This is unaffected by the
   server-side alias and works fine as-is; short CLI subcommand aliases would
   be a separate additive change if wanted later.
3. **Full doc rewrite**: existing curl examples in `README.md` and
   `docs/user-guide.md` still use the long-form paths intentionally (both
   forms work); only a short note was added rather than rewriting every
   example.

## How to verify manually

```bash
go run ./cmd/server serve --port 8090 --database-url="file:hpc_test.db?cache=shared&_fk=1" --secrets-file ./secrets.json

# In another shell:
curl -sS http://127.0.0.1:8090/campaigns/ | jq
curl -sS http://127.0.0.1:8090/jobs/ | jq
# Should return identical results to:
curl -sS http://127.0.0.1:8090/firmwareupdatecampaigns/ | jq
curl -sS http://127.0.0.1:8090/firmwareupdatejobs/ | jq
```
