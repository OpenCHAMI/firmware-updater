# Firmware Search and Replace Plan

## Goal

Resolve repository firmware images for every readable Redfish inventory component, then create campaign jobs that either report the component is already current, request an update, or fail with a clear reason when no repository image can be found.

## Implementation Plan

1. Match each Redfish inventory component to a firmware registry record by exact target identity.
	- Use either the inventory component `Id` or `Name` to match the registry target.
	- Create a job for every readable inventory component, including components for which no image resolves.

2. Determine the firmware identity constraint for the matched component.
	- When Redfish provides `SoftwareId`, require the repository record to match it and do not use the device model.
	- When `SoftwareId` is absent, resolve the device profile and read the model through its configured model path and field.
	- Require the repository record to match the resolved model in this fallback case.

3. Resolve the selected firmware image.
	- Filter firmware records by target plus software ID, or target plus model.
	- Select the latest semantic version from the remaining candidates.
	- Create a failed job with `firmware image not found in repository` when no candidate remains.

4. Create jobs based on the inventory and selected image versions.
	- Compare Redfish inventory and repository image versions as trimmed strings.
	- Mark equal versions completed with `already at the requested version`.
	- Create an update job for every version mismatch.
	- Allow terminal jobs to release the per-device update slot so update jobs run sequentially without being blocked by completed or failed jobs.

5. Maintain job and campaign status output.
	- Include Redfish `target`, `currentVersion`, and status `message` in campaign child job summaries.
	- Include `updateVersion` from the selected repository record's `versionString` for update jobs only.
	- Keep `resolvedVersion` internal for semantic Redfish completion verification; do not expose it in job output.
	- Clear status details that no longer apply when a job changes state.
	- Expose failure details only for failed jobs, messages only for completed jobs, and never expose `updateVersion` for failed jobs.
	- Omit blank `errorDetail`, `message`, and `updateVersion` fields from campaign child output.
	- Notify the owning campaign on child-job status changes and reload child jobs immediately before saving the campaign summary.

## Implementation Locations

- `pkg/reconcilers/firmwareupdatecampaign_reconciler.go`: match device profiles, read the model, and pass inventory target and software ID to the resolver.
- `pkg/firmwareproxy/resolver.go`: filter firmware records by target and software ID or model, then select the newest semantic version.
- `pkg/firmwareproxy/resolver_test.go`: cover model matching, software-ID precedence, and latest-version selection.

## Validation Plan

1. Run `go test ./pkg/firmwareproxy` to verify resolver filtering and semantic-version selection.
2. Run `go test ./pkg/reconcilers` to verify campaign job creation and status propagation.
3. Where SQLite-backed reconciliation tests are enabled, ensure CGO is available because `go-sqlite3` requires it.

## Completion Criteria

- Every readable Redfish inventory component produces a campaign job.
- Firmware selection follows target-plus-software-ID precedence, with target-plus-model fallback.
- The latest matching semantic version is selected.
- Job and campaign status output exposes the required fields without stale or inapplicable details.
