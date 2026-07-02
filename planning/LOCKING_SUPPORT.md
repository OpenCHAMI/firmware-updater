# Lock Integration for Firmware Update Jobs

## 1. Context and Rationale

This is a follow-on plan to `GROUP_SELECTION.md`. Group-based target selection
resolves a `FirmwareUpdateJob` into a set of member BMCs and dispatches updates
with bounded parallelism. That plan deliberately defers lock handling; this plan
adds it.

The goal is to gate firmware dispatch on SMD lock/reservation state so that a job
does not flash a node that an operator or another service has administratively
locked or reserved. SMD already maintains component lock and reservation state;
this plan consumes that state to make a pre-dispatch safety decision. It does not
acquire or release locks/reservations.

Dependencies and scope boundaries:

- Builds on the group resolution implemented in `GROUP_SELECTION.md`; lock checks
  apply to both single-`TargetAddress` and group (`GroupRef`) jobs.
- Lock state is read-only. Actively acquiring a reservation for the duration of an
  update (via `/hsm/v2/locks/service/reservations`) is explicitly out of scope and
  may be a further follow-on.
- No new BMC credential handling is introduced here.

## 2. Schema Modifications

No new spec selector fields are required. Lock checking is implicit behavior gated
on resolved targets. The following optional fields control lock policy.

### 2.1 New Spec Fields

1. `IgnoreLocks` (bool, optional, default false)
   - If false (default): a job fails if any in-scope member is locked or reserved.
   - If true: lock/reservation state is recorded in status but does not block
     dispatch. Intended for break-glass/admin use only.

### 2.2 New Status Fields

1. `LockConflicts` ([]string, optional)
   - Member xnames found locked or reserved at the pre-dispatch check, with the
     conflicting flag and lock timing detail for operator triage.

## 3. Lock Check and Reconciliation

Implement the lock gate in `pkg/reconcilers/firmwareupdatejob_reconciler.go`, after
target resolution and before per-BMC dispatch, with SMD helper logic alongside the
group-resolution helpers.

### 3.1 Sequence

1. Resolve the target set (single `TargetAddress`, or group members per
   `GROUP_SELECTION.md`).
2. `POST /hsm/v2/locks/status` with body `{ "ComponentIDs": ["<member_xname>", ...] }`
   (the member xnames from resolution, not the BMC FQDNs).
3. Read the `Components[]` array. Treat a member as not-safe-to-update when
   `Locked == true` OR `Reserved == true`.
   - `Locked` is the administrative lock flag.
   - `Reserved` indicates an ownership reservation held via the separate
     reservations API.
4. If any in-scope member is locked or reserved and `IgnoreLocks=false`, set the job
   to `Failed` with lock conflict detail (include the member xname and the
   `CreationTime`/`ExpirationTime` from its lock entry). Record all conflicts in
   `LockConflicts`.
5. The `POST` variant also returns `NotFound[]` for unknown xnames; surface these in
   status for triage.
6. If `IgnoreLocks=true`, record any conflicts in `LockConflicts` and proceed.

### 3.2 Error Handling

**Terminal Errors (fail job immediately):**
- Any in-scope member locked or reserved while `IgnoreLocks=false`.

**Transient Errors (exponential backoff retry):**
- SMD lock-status query timeout or 5xx response.

Lock conflicts are treated as terminal for the current reconcile pass unless policy
(an open item) dictates retry/wait-for-unlock behavior.

## 4. SMD API Contract (Validated)

Validated against the OpenCHAMI/smd source (`master`). Base path:
`apiRootV2 = "/hsm/v2"` (`cmd/smd/main.go`).

### 4.1 Lock Query

- **Endpoint:** `POST /hsm/v2/locks/status` (body filter; preferred) or
  `GET /hsm/v2/locks/status` (query filter).
- **Handler:** `doCompLocksStatus` / `doCompLocksStatusGet` (`cmd/smd/smd-api.go`);
  routes under `compLockBaseV2 = "/hsm/v2/locks"`.
- **Request body:** `sm.CompLockV2Filter` (`pkg/sm/complocks.go`):
  `{ "ComponentIDs": ["x..."] }` (plus optional Type/State/Group/Partition filters).
- **Response type:** `sm.CompLockV2Status` (`pkg/sm/complocks.go`):
  - `Components[]` with `ID`, `Locked`, `Reserved`, `ReservationDisabled`,
    `CreationTime`, `ExpirationTime`.
  - `NotFound[]` (POST variant only).
- **Locks vs reservations:** distinct. `Locked` = administrative lock; `Reserved` =
  ownership reservation via the separate `/hsm/v2/locks/reservations*` and
  `/hsm/v2/locks/service/reservations*` APIs (which carry DeputyKey/ReservationKey).
  For a pre-update safety gate, treat `Locked || Reserved` as "do not update".

### 4.2 Caveats and Open Items

1. **Reservations not acquired:** this design only reads lock/reservation state;
   holding a reservation for the update duration is a separate follow-on.
2. **Auth:** the firmware-updater -> SMD authentication model (token passthrough vs.
   service token) is shared with `GROUP_SELECTION.md` and must be decided there.

## 5. Acceptance Criteria

1. **Code Compilation:**
   - `fabrica generate`, `go mod tidy`, `go build ./...` complete without error.

2. **Lock Gating:**
   - A job whose target set includes a locked or reserved member is set to `Failed`
     with lock conflict detail when `IgnoreLocks=false`.
   - The same job proceeds, recording `LockConflicts`, when `IgnoreLocks=true`.
   - Lock check applies to both single-`TargetAddress` and group jobs.

3. **Status Reporting:**
   - `LockConflicts` captures conflicting member xnames and lock timing.
   - `NotFound[]` xnames are surfaced in status.

4. **Unit Tests:**
   - Lock conflict detection (locked, reserved, both, neither).
   - `IgnoreLocks` true/false behavior.
   - SMD lock-status query error handling.

## 6. Open Decision Items

1. **Lock vs. reservation policy**
   - Confirm that both `Locked` and `Reserved` block dispatch, or whether reserved
     (but unlocked) members should be treated differently.

2. **Retry/wait-for-unlock**
   - Decide whether a lock conflict is strictly terminal or should retry with
     backoff until a deadline.

3. **Group-level vs. node-level locks**
   - Confirm whether group-level lock state should be honored in addition to
     per-member node locks.

4. **Reservation acquisition**
   - Decide whether a future follow-on should hold a reservation for the duration of
     the update.

## 7. Output Artifacts

Upon successful implementation, generate a handoff document
(`HANDOFF_LOCKING_SUPPORT.md`) containing:

1. Summary of the lock gate behavior and where it sits in the reconcile flow.
2. Exact verified `curl` demonstrating a job blocked by a lock conflict and one
   proceeding with `IgnoreLocks=true`.
3. Operational notes for troubleshooting lock conflicts (how to read
   `LockConflicts`, how to query SMD lock status directly).
4. SMD integration details: exact lock endpoints called and contract assumptions
   verified.
