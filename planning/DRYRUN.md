## 1. Scope and Intent

Add a `dryrun` capability to both `FirmwareUpdateCampaign` and `FirmwareUpdateJob` so the reconciliation flow executes normally but does not send the Redfish update command when dry run is enabled.

Functional intent:

- If `dryrun=true`, perform all normal preparation and validation steps, but skip the outbound Redfish `SimpleUpdate` POST to the target device.
- When the command would have been sent successfully from a precondition standpoint, mark the job as successful.
- Record a job message that includes:
  - the exact payload that would have been sent
  - the device URI/endpoint that would have received it
- If `dryrun` is not provided, default behavior is `false`.

---

## 2. Data Model and API Plan

### 2.1 FirmwareUpdateJob

Add optional field:

- `spec.dryrun` (boolean, optional, default false)

Behavioral notes:

- Omitted value must be treated as `false`.
- Explicit `true` activates no-op dispatch mode.

### 2.2 FirmwareUpdateCampaign

Add optional field:

- `spec.dryrun` (boolean, optional, default false)

Propagation rule:

- Every campaign-generated child `FirmwareUpdateJob` inherits `spec.dryrun` from its parent campaign unless the project already supports explicit per-job overrides.

### 2.3 OpenAPI / Generated Types

Update API schema and regenerate server/client models so `dryrun` is represented consistently in:

- API spec
- generated request/response models
- reconciler-visible structs

Implementation preference:

- Use pointer booleans (`*bool`) in generated/spec-facing structs if needed to preserve “not provided” semantics, while effective runtime value resolves to `false`.

---

## 3. Reconciler Behavior Plan

### 3.1 Job Reconciler Flow

In `FirmwareUpdateJob` reconciliation:

1. Execute normal pre-dispatch steps (state checks, OCI resolution/proxy URI generation, target validation, payload construction).
2. Build the Redfish update request payload exactly as in non-dry-run mode.
3. Resolve effective dry-run flag:
	- `effectiveDryRun = spec.dryrun` when provided
	- otherwise `effectiveDryRun = false`
4. Branch behavior:
	- If `effectiveDryRun=false`: send Redfish command with current logic.
	- If `effectiveDryRun=true`: skip HTTP POST entirely.
5. For dry-run path, set terminal success status equivalent to a successfully dispatched job.
6. Persist informative status message containing:
	- target device update URI
	- JSON payload that would have been sent

### 3.2 Campaign Reconciler Flow

In `FirmwareUpdateCampaign` reconciliation:

1. Resolve effective campaign dry-run flag with default false.
2. When constructing desired child jobs, copy this value into each child job spec.
3. Keep sequencing/concurrency logic unchanged; dry run only affects device dispatch inside job reconciler.

---

## 4. Status and Message Contract

### 4.1 Success Semantics

For dry-run jobs, treat “command would have been sent” as success when all of the following are true:

- payload construction succeeds
- required target/device URI is resolved
- no validation/precondition failures occur before dispatch

Result:

- mark job state as successful/completed using the project’s existing terminal success state.

### 4.2 Message Format

Store a deterministic, human-readable message in job status. Suggested format:

`Dry-run enabled: skipped Redfish SimpleUpdate POST to <device_update_uri>; payload=<json_payload>`

Requirements:

- Include full device URI used for update action.
- Include serialized payload body exactly as constructed.
- Keep message safe for logs (do not include secrets if credentials are present elsewhere).

---

## 5. Error Handling Expectations

Dry run does not suppress non-dispatch errors. Fail normally when:

- required spec fields are invalid/missing
- OCI resolution/proxy preparation fails
- payload generation fails

Only the outbound Redfish command is skipped.

---

## 6. Implementation Steps

1. Update API schema for campaign/job to include optional `dryrun` boolean.
2. Regenerate generated models and handlers impacted by schema.
3. Update campaign reconciler job-construction logic to propagate `dryrun`.
4. Update job reconciler dispatch logic with dry-run branch.
5. Add/update status message helper for consistent dry-run message text.
6. Add unit tests for default behavior, propagation, and success/message behavior.

---

## 7. Test Plan

### 7.1 Unit Tests

- Job defaults to non-dry-run when field omitted.
- Job with `dryrun=true` does not invoke Redfish POST client.
- Job with `dryrun=true` is marked success/completed when pre-dispatch steps pass.
- Job dry-run status message includes both URI and payload.
- Campaign with omitted `dryrun` creates child jobs with `dryrun=false` behavior.
- Campaign with `dryrun=true` creates child jobs carrying `dryrun=true`.

### 7.2 Regression Tests

- Existing non-dry-run job path still sends Redfish POST unchanged.
- Existing campaign sequencing behavior remains unchanged.

### 7.3 Optional Integration Validation

- Create one dry-run campaign and verify all produced jobs complete without contacting device endpoints.
- Verify each completed job status message includes URI and payload preview.

---

## 8. Acceptance Criteria

1. `dryrun` exists on both campaign and job specs as optional boolean.
2. Omitted `dryrun` behaves identically to `false`.
3. Campaign-provisioned jobs inherit campaign `dryrun` value.
4. Dry-run jobs skip outbound Redfish update command.
5. Dry-run jobs are marked successful when command preconditions are satisfied.
6. Job status message includes both would-send payload and device URI.
7. Existing non-dry-run behavior and sequencing are unchanged.

---

## 9. Output Artifacts

Create a handoff file named `HANDOFF-DRYRUN.md` in the planning directory after implementation is complete.

Required contents:

1. A concise summary of the dry-run behavior implemented for campaign and job.
2. A list of all code changes made, including each touched file and what changed.
3. Notes on validation performed (tests/commands run) and results.
