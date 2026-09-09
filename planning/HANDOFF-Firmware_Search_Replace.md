# Firmware Search Replacement Handoff

## Implemented behavior

Universal firmware discovery now selects registry firmware records using exact Redfish and device-profile identity data:

- Every firmware record must match either the Redfish inventory component `Id` or `Name` against its registry target.
- When the inventory record contains `SoftwareId`, the firmware record must also match that software ID. The device model is not used in this case.
- When the inventory record has no `SoftwareId`, the firmware record must match the model obtained using the matched device profile's configured model path and field.
- When multiple matching records exist, the latest semantic version is selected.
- A firmware job is created for every readable Redfish inventory component. Missing matching images create a failed job with the reason `firmware image not found in repository`.
- The selected repository image and Redfish inventory versions are compared as trimmed strings. Equal strings create a completed job with the message `already at the requested version`; every mismatch creates an update job.
- Terminal jobs do not occupy the per-device update slot; update jobs continue to run sequentially for a device.
- Campaign `status.childJobs` entries include each job's Redfish `target` and status `message`.
- Job and campaign child output include `currentVersion`; update jobs also include `updateVersion` from the repository `versionString` record field.
- `resolvedVersion` remains internal for semantic Redfish completion verification and is not included in firmware job output.
- Child job status changes notify the owning campaign immediately, keeping `childJobs` messages and update versions current between periodic reconciliations.
- Job transitions clear incompatible status details. Campaign child output only shows failure details for failed jobs, messages for completed jobs, and never exposes `updateVersion` for failed jobs.
- Campaign child output omits blank `errorDetail`, `message`, and `updateVersion` values. The campaign reloads child jobs immediately before saving its summary to avoid stale fields overwriting current job data.

## Implementation locations

- `pkg/reconcilers/firmwareupdatecampaign_reconciler.go` matches the device profile, reads the model, and forwards each inventory member's target and software ID to the resolver.
- `pkg/firmwareproxy/resolver.go` performs the target-plus-model or target-plus-software-ID filtering before selecting the latest available version.
- `pkg/firmwareproxy/resolver_test.go` covers model matching, software-ID precedence, and latest-version selection.

## Validation

- `pkg/firmwareproxy/resolver_test.go`: 21 tests passed.
- `pkg/reconcilers/firmwareupdatecampaign_reconciler_test.go`: 14 tests passed; one existing SQLite-backed test could not initialize because this environment has `CGO_ENABLED=0`, while `go-sqlite3` requires CGO.
