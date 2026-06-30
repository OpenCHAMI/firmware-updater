# HANDOFF_GROUP_SELECTION

## 1) Scope and Context

This phase delivers group-based firmware update orchestration by extending FirmwareUpdateJob.
The goal is to support cabinet-style rollouts through SMD-backed user-defined groups without requiring users to submit per-node targets.

Current behavior is primarily single-target oriented. This plan adds fan-out target resolution and bounded parallel dispatch while preserving existing reconciliation patterns. Lock/reservation gating is handled in a follow-on plan (`LOCKING_SUPPORT.md`).

## 2) Planning Decisions Already Agreed

1. Primary scope is cabinet update behavior implemented through user-defined groups.
2. Firmware-updater consumes existing groups; it does not create or mutate groups in this phase.
3. Group source is SMD group APIs.
4. Job model extends existing FirmwareUpdateJob.
5. Credential input moves toward secret references in spec.
6. Lock/reservation gating is out of scope for this phase and is deferred to `LOCKING_SUPPORT.md`.
7. Success criteria for a job is all targeted nodes succeed.
8. Testing target for this phase is minimal unit tests only.

## 3) Proposed API Contract Changes

Update FirmwareUpdateJob spec to support group fan-out while preserving backward compatibility.

### 3.1 Spec additions

1. groupRef (string, optional)
- User-provided SMD group identifier.

2. credentialsRef (object, optional)
- Reference to secret material used to resolve per-target BMC credentials.
- Suggested fields:
  - provider (string)
  - reference (string)

3. maxParallel (int, optional)
- Maximum concurrent node updates within a job.

4. allowPartialTargets (bool, optional, default false)
- If false, reconciliation fails when group membership cannot be fully resolved.

### 3.2 Validation rules

1. Exactly one target selector must be set:
- Explicit targets, or
- groupRef

2. credentialsRef is required for groupRef mode unless an approved fallback exists.

3. maxParallel must be >= 1.

4. Existing explicit-target workflow remains valid.

## 4) Reconciler Execution Plan

Implement in pkg/reconcilers/firmwareupdatejob_reconciler.go with helper logic in pkg/firmwareproxy and/or internal services.

### 4.1 State model

Use existing lifecycle states and add deterministic handling for fan-out execution progress.

1. Pending
2. Resolving
3. InProgress
4. Completed
5. Failed

### 4.2 Resolution sequence

1. Resolve firmware payload from OCI as currently implemented.
2. Resolve node set from SMD groupRef.
3. Resolve credentials from credentialsRef.
4. Build execution plan with bounded parallelism.
5. Dispatch per-node Redfish update.
6. Aggregate per-node outcomes and finalize status.

### 4.3 Failure policy

1. Terminal errors
- groupRef not found
- no resolved members
- credential reference invalid or unreadable

2. Transient errors
- SMD query timeout/5xx
- Redfish transport timeout/5xx
- temporary secret backend unavailability

3. Job result rule
- Any target failure causes overall job failure (all-targets-must-succeed).

## 5) SMD Integration Requirements

1. Query group membership via SMD group APIs.
2. Resolve each member to BMC address and required metadata.
3. Support incomplete metadata detection with explicit status/error details.
4. Capture enough diagnostic context in status.errorDetail for operator triage.

## 6) Lock Integration Requirements

Lock/reservation gating is deferred to a follow-on plan: see `LOCKING_SUPPORT.md`
(and its handoff `HANDOFF_LOCKING_SUPPORT.md`). This phase resolves and dispatches
to the target set without consulting SMD lock state. The lock follow-on layers a
pre-dispatch safety gate on top of the resolution delivered here.

## 7) Credentials Strategy (TBD Backend)

This phase requires credential references in API, but backend implementation is still undecided.

### 7.1 Minimum implementation requirement

1. Define backend-agnostic credentialsRef schema.
2. Implement resolver interface with pluggable providers.
3. Provide one concrete provider once backend choice is finalized.

### 7.2 Security requirements

1. Never persist raw credentials in resource spec or status.
2. Avoid logging secrets at all levels.
3. Scope credentials to required target registry/BMC operations only.

## 8) Acceptance Criteria

1. API and generated artifacts compile after schema updates.
2. Validation enforces selector exclusivity and group mode requirements.
3. Group-based target resolution from SMD works for a valid groupRef.
4. Fan-out execution respects maxParallel bound.
5. Job fails when any target fails; succeeds only when all targets succeed.
6. Existing explicit-target jobs continue to function unchanged.
7. Minimal unit tests added for:
- validation rules
- group resolution behavior
- fan-out result aggregation

## 9) Implementation Work Breakdown

1. API/model updates
- apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go
- regenerate with fabrica generate

2. Reconciler and service logic
- pkg/reconcilers/firmwareupdatejob_reconciler.go
- helper modules for SMD lookup, credential resolution

3. Server wiring/config
- cmd/server/main.go for any new integration config

4. Tests
- unit tests under pkg/reconcilers and helper packages

## 10) Open Items Requiring Additional Information

1. Secret backend decision
- Choose provider: Kubernetes Secrets, Vault, or backend-agnostic first implementation.

2. Default maxParallel value
- Decide operational default when field omitted.

3. Cabinet-to-group helper ownership
- Confirm whether helper utility belongs in firmware-updater or Magellan.

4. Partial membership policy
- Confirm behavior when expected cabinet members are absent from group.

## 11) Output Artifact Requirements (After Implementation)

After implementation, generate a handoff report in this planning directory containing:

1. Brief summary of implemented group update behavior.
2. Exact verified create command used for a group-based job.
3. Operational notes for running and troubleshooting group fan-out updates.
