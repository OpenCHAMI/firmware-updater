# Target Selection via SMD User-Defined Groups

## 1. Context and Rationale

Currently, `FirmwareUpdateJob` targets a single BMC: `Spec.TargetAddress` identifies one BMC, and `Spec.Targets` / `Spec.Component` select which Redfish firmware-inventory components to update *on that one BMC*. There is no way to address many BMCs in one job. For cabinet-scale deployments, issuing one job per BMC is operationally unwieldy.

SMD already maintains hierarchical grouping and group-membership APIs. This plan adds a way to specify the *set of BMCs* via an SMD group reference, allowing a user to define a logical group (e.g., "cabinet-x1000") and update all member BMCs in a single job request. Per-BMC component selection (`Targets` / `Component`) is unchanged and continues to apply to each resolved member.

Key constraint: firmware-updater will consume existing groups only; it does not create or mutate groups. Group lifecycle is managed separately (e.g., via Magellan or manual SMD administration).

Note on credentials: BMC authentication is out of scope for this plan and is handled separately. This plan does not introduce or modify credential fields.

## 2. Schema Modifications

Extend the `FirmwareUpdateJobSpec` to support group-based BMC selection while preserving backward compatibility with the existing single-`TargetAddress` mode.

### 2.1 New Fields

Add the following optional fields to `FirmwareUpdateJobSpec`:

1. `GroupRef` (string, optional)
   - Identifier of an SMD user-defined group. The group's members identify the set of BMCs to update.
   - This is an alternative to the single `TargetAddress`; it selects *which BMCs*, not which Redfish components.
   - Example: "cabinet-x1000", "compute-nodes-row-2"

2. `MaxParallel` (int, optional)
   - Maximum number of member BMCs to update concurrently within a single job.
   - Default: TBD (open decision item).
   - Minimum: 1.

3. `AllowPartialTargets` (bool, optional, default false)
   - If false: reconciliation fails if group membership cannot be fully resolved (any member's BMC address is missing or invalid).
   - If true: reconciliation proceeds with the resolvable members; unresolvable members are recorded but do not fail the job.

### 2.2 Required Schema Change

The current schema marks `TargetAddress` as `validate:"required"`. To support group mode, `TargetAddress` must become conditionally required:

- Make `TargetAddress` optional at the struct-tag level.
- Enforce the selector rule in the resource's `Validate` method (see 2.3).
- `Targets` and `Component` keep their existing semantics and validation; they continue to describe per-BMC component selection that is applied to every resolved member.

### 2.3 Validation Rules

1. **BMC Selector Exclusivity:**
   - Exactly one of the following must be set:
     - `TargetAddress` (existing single-BMC field), or
     - `GroupRef` (new field)
   - `Validate` must reject jobs with both or neither.

2. **Component Selection Still Required:**
   - The existing rule stands: at least one of `Targets` or `Component` must be provided, and it applies to every resolved member in group mode.

3. **MaxParallel Constraints:**
   - If present, must be >= 1.
   - `MaxParallel` is only meaningful in group mode; ignore it (or reject it) in single-`TargetAddress` mode.

4. **Backward Compatibility:**
   - Existing single-`TargetAddress` jobs continue to work unchanged.
   - No changes to `Targets`, `Component`, `OCIReference`, or `Discovery` semantics.

## 3. SMD Integration and Reconciliation

Implement group resolution logic in `pkg/reconcilers/firmwareupdatejob_reconciler.go` with helper functions in `internal/smd` or similar.

### 3.1 SMD Query Contract

All SMD calls use base path `/hsm/v2` (validated; see Section 7).

1. Fetch the group and its members:
   - `GET /hsm/v2/groups/{GroupRef}` and read `members.ids[]` (HMS component xnames, conventionally Node xnames).
   - Optionally scope with `?partition=<name>`.
2. Resolve each member xname to its controlling BMC's Redfish address:
   - `GET /hsm/v2/Inventory/ComponentEndpoints/{member_xname}` and read `RedfishEndpointFQDN` (BMC FQDN to dispatch Redfish to) and `RedfishEndpointID` (BMC xname, for logging/dedup).
   - Do not derive the BMC address by xname string manipulation; SMD inventory is authoritative. xname parent derivation is a fallback only.
3. A member with no ComponentEndpoint (e.g., not yet discovered) is treated as unresolvable, not an error; handling depends on `AllowPartialTargets`.

### 3.2 Resolution Sequence

1. **Single-BMC Path (existing behavior):**
   - If `Spec.TargetAddress` is set, proceed with current logic unchanged.
   - SMD resolution and parallelism do not apply.

2. **Group Path (new behavior):**
   - If `Spec.GroupRef` is set:
     a. State transitions to `Resolving`.
     b. `GET /hsm/v2/groups/{GroupRef}` to fetch `members.ids[]`. A 404 ("No such group") is a terminal error surfaced distinctly.
     c. For each member xname, `GET /hsm/v2/Inventory/ComponentEndpoints/{xname}` and use `RedfishEndpointFQDN` as that member's BMC address (the per-member `TargetAddress` equivalent).
     d. If no members resolve and `AllowPartialTargets=false`, set state to `Failed` with error detail "group 'X' returned no resolvable members".
     e. If some members are unresolvable and `AllowPartialTargets=true`, record them and proceed with the resolvable members.
     f. Resolve the OCI payload once (shared across all members), then perform the existing per-BMC dispatch (including `Targets` / `Component` discovery) for each resolved member.

3. **Fan-Out Execution:**
   - Resolve the OCI payload a single time before fan-out; reuse the resolved digest for every member.
   - If `MaxParallel` < resolved member count, process members in bounded waves: no more than `MaxParallel` concurrent per-BMC dispatches at once.
   - Apply the existing per-BMC reconcile path (action-URI discovery, component resolution, `SimpleUpdate` dispatch) to each member.
   - Aggregate per-member success/failure.
   - Set overall job state based on the aggregate result.

### 3.3 Error Handling

**Terminal Errors (fail job immediately):**
- GroupRef not found in SMD.
- SMD returns group with zero members and `AllowPartialTargets=false`.
- SMD response is malformed or unreadable.
- BMC selector validation fails (both or neither of `TargetAddress` / `GroupRef`).

**Transient Errors (exponential backoff retry):**
- SMD query timeout or 5xx response.
- Redfish dispatch timeout or 5xx response for an individual member BMC.

**Result Aggregation Rule:**
- Any member BMC update failure causes overall job failure.
- Success requires all member BMCs to succeed.

## 4. Status Field Enhancements

Extend `FirmwareUpdateJobStatus` to track group-based execution progress.

1. `ResolutionDetail` (string, optional)
   - Captures details of group member resolution for debugging (e.g., "resolved 5 of 5 members", "1 member unresolvable").

2. `MemberCount` (int, optional)
   - Number of member BMCs selected (1 in single-`TargetAddress` mode, N in group mode).

3. `CompletedCount` (int, optional)
   - Number of member BMCs completed successfully (for fan-out jobs).

4. `FailedMembers` ([]string, optional)
   - List of member BMC addresses that failed during fan-out execution.

## 5. Lock Integration (SMD)

Before dispatching to the resolved member set, query SMD lock status and gate execution.

1. `POST /hsm/v2/locks/status` with body `{ "ComponentIDs": ["<member_xname>", ...] }` (the member xnames from group resolution, not the BMC FQDNs).
2. Read the `Components[]` array. Treat a member as not-safe-to-update when `Locked == true` OR `Reserved == true`.
   - `Locked` is the administrative lock flag.
   - `Reserved` indicates an ownership reservation held via the separate reservations API.
3. If any in-scope member is locked or reserved, set the job to `Failed` with lock conflict detail (include the member xname and the `CreationTime`/`ExpirationTime` from its lock entry).
4. The `POST` variant also returns `NotFound[]` for unknown xnames; surface these in status for triage.

Note: this design only reads lock/reservation state. Actively acquiring a reservation for the duration of the update (via `/hsm/v2/locks/service/reservations`) is a separate follow-on, out of scope here.

## 6. Acceptance Criteria

1. **Code Compilation:**
   - `fabrica generate`, `go mod tidy`, `go build ./...` complete without error.

2. **Validation:**
   - Requests with both `TargetAddress` and `GroupRef` are rejected with HTTP 400.
   - Requests with neither `TargetAddress` nor `GroupRef` are rejected with HTTP 400.
   - Requests with `GroupRef` but no `Targets`/`Component` are rejected with HTTP 400.
   - MaxParallel < 1 is rejected with HTTP 400.

3. **Group Resolution:**
   - For a valid groupRef with N members, the reconciler correctly resolves all N member BMC addresses.
   - Parallelism bound is honored: no more than MaxParallel concurrent per-BMC dispatches occur within a single job.

4. **Error Handling:**
   - Missing group returns terminal error with detail "group 'X' not found".
   - Empty group + AllowPartialTargets=false returns terminal error.
   - Partial group + AllowPartialTargets=true records unresolvable members and proceeds.

5. **Backward Compatibility:**
   - Single-`TargetAddress` jobs execute unchanged.
   - No regression in existing single-BMC reconciliation logic.

6. **Job Completion:**
   - Group-based job state reflects the all-members-must-succeed rule.
   - Status fields capture resolution and execution detail.

7. **Unit Tests:**
   - Validation selector rules (`TargetAddress` XOR `GroupRef`; component selection required).
   - SMD group resolution happy path and error cases.
   - Parallelism wave/bounding logic.
   - Lock conflict detection.
   - Backward compatibility with single-`TargetAddress` jobs.

### 6.1 Example Group-Mode Request

Create a firmware update job that targets an SMD group instead of a single BMC:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "group-update-cabinet-x1000"},
    "spec": {
      "groupRef": "cabinet-x1000",
      "ociReference": "127.0.0.1:5000/firmware/bios:1.8.2",
      "serverProxyAddress": "10.254.1.20",
      "component": "BIOS",
      "maxParallel": 5
    }
  }'
```

Expected behavior:

1. Service queries SMD for members of `cabinet-x1000` and resolves each to a BMC address.
2. The OCI payload is resolved once and reused for all members.
3. Each member BMC runs the existing component discovery and `SimpleUpdate` dispatch, in waves of at most `maxParallel`.
4. Job state reflects the all-members-must-succeed rule; status records `MemberCount`, `CompletedCount`, and any `FailedMembers`.

## 7. SMD API Contract (Validated)

Validated against the OpenCHAMI/smd source (`master`). Base path: `apiRootV2 = "/hsm/v2"` (`cmd/smd/main.go`). Routes in `cmd/smd/routers.go`; handlers in `cmd/smd/smd-api.go`.

### 7.1 Group Fetch

- **Endpoint:** `GET /hsm/v2/groups/{group_label}` (full group) or `GET /hsm/v2/groups/{group_label}/members` (members only).
- **Handler:** `doGroupGet` / `doGroupMembersGet` (`cmd/smd/smd-api.go`).
- **Response type:** `sm.Group` (`pkg/sm/groups.go`):
  - `label`, `description`, `exclusiveGroup` (omitempty), `tags` (omitempty), `members.ids[]`.
- **Members:** `members.ids[]` are normalized HMS component xnames, conventionally Node xnames (e.g., `x3000c0s1b0n0`) — not BMC xnames. SMD does not restrict membership to one component type, so member type may be heterogeneous (see 7.4 caveats).
- **Query params:** `?partition=<name>` filters members by partition (`NULL` matches members in no partition).
- **Errors:** 404 `"No such group"` when the label is unknown.

### 7.2 Member xname -> BMC Resolution

- **Primary endpoint:** `GET /hsm/v2/Inventory/ComponentEndpoints/{xname}` (single) or `GET /hsm/v2/Inventory/ComponentEndpoints` (collection).
- **Handler:** `doComponentEndpointGet` / `doComponentEndpointsGet` (`cmd/smd/smd-api.go`).
- **Response type:** `sm.ComponentEndpoint` (`pkg/sm/endpoints.go`) embedding `rf.ComponentDescription` (`pkg/redfish/rfcomponents.go`). Key fields:
  - `RedfishEndpointFQDN` (outer `sm.ComponentEndpoint.RfEndpointFQDN`) — the BMC FQDN to send Redfish to. **Use this as the per-member dispatch address.**
  - `RedfishEndpointID` (`rf.ComponentDescription.RfEndpointID`) — the controlling BMC xname (logging/dedup).
  - `RedfishURL` (outer `URL`) — full Redfish URL of the component.
- **Corroborating endpoint:** `GET /hsm/v2/Inventory/RedfishEndpoints/{bmc_xname}` → `rf.RedfishEPDescription` with `ID`, `Type`, `Hostname`, `Domain`, `FQDN`, `IPAddress` (struct `IPAddr`). Keyed by BMC xname; useful when an IP (not FQDN) is required.
- **Authority:** SMD inventory FQDN/IP is authoritative. xname parent derivation (`x3000c0s1b0n0` -> `x3000c0s1b0`) is a fallback only.

### 7.3 Lock Query

- **Endpoint:** `POST /hsm/v2/locks/status` (body filter; preferred) or `GET /hsm/v2/locks/status` (query filter).
- **Handler:** `doCompLocksStatus` / `doCompLocksStatusGet` (`cmd/smd/smd-api.go`); routes under `compLockBaseV2 = "/hsm/v2/locks"`.
- **Request body:** `sm.CompLockV2Filter` (`pkg/sm/complocks.go`): `{ "ComponentIDs": ["x..."] }` (plus optional Type/State/Group/Partition filters).
- **Response type:** `sm.CompLockV2Status` (`pkg/sm/complocks.go`):
  - `Components[]` with `ID`, `Locked`, `Reserved`, `ReservationDisabled`, `CreationTime`, `ExpirationTime`.
  - `NotFound[]` (POST variant only).
- **Locks vs reservations:** distinct. `Locked` = administrative lock; `Reserved` = ownership reservation via the separate `/hsm/v2/locks/reservations*` and `/hsm/v2/locks/service/reservations*` APIs (which carry DeputyKey/ReservationKey). For a pre-update safety gate, treat `Locked || Reserved` as "do not update".

### 7.4 Caveats and Open Items

1. **Auth:** SMD handlers in `cmd/smd/smd-api.go` do not themselves enforce authorization; OpenCHAMI deployments typically front SMD with a JWT/gateway. The firmware-updater -> SMD authentication model (token passthrough vs. service token) must be decided. Tracked in Section 8.
2. **Member type heterogeneity:** groups may contain non-Node xnames. Define policy: resolve only Nodes, or accept controller xnames directly when `Type` indicates a BMC/controller.
3. **No pagination:** these GET collections return full arrays; filter via query params only. Acceptable for expected group sizes but note for very large groups.
4. **Stale inventory:** a member present in a group but not yet discovered will lack a ComponentEndpoint; handle as "unresolved," not an error (ties to `AllowPartialTargets`).
5. **Reservations not acquired:** this design only reads lock/reservation state; holding a reservation for the update duration is a separate follow-on.

## 8. Open Decision Items

The following items require clarification before implementation begins:

1. **Default MaxParallel Value**
   - Candidates: 1 (serial), 5, 10, or operator-specified only.
   - Recommendation: defer to operator choice; require explicit value in job spec if this is critical for deployment.

2. **Cabinet-to-Group Helper Utility**
   - Should auto-assignment of cabinet members to a group live in firmware-updater, Magellan, or elsewhere?
   - This plan does not implement auto-assignment; it provides a separate handoff task.

3. **Partial Membership Policy**
   - Should `AllowPartialTargets=true` also log warnings to an external event system, or console logging only?

4. **SMD Base URL and Authentication**
   - Firmware-updater needs an SMD base URL config value (default `/hsm/v2` on the SMD host) and an auth model. SMD handlers do not enforce authz directly; deployments front SMD with a JWT/gateway.
   - Decide token passthrough vs. dedicated service token for firmware-updater -> SMD calls.

5. **Member Type Policy**
   - Define behavior when a group contains non-Node xnames (switches, controllers, BMC xnames). Options: resolve only Nodes, or accept controller/BMC xnames directly when `Type` indicates a controller.

## 9. Output Artifacts

Upon successful implementation, generate a handoff document (`HANDOFF_GROUP_SELECTION.md`) containing:

1. **Summary of Group Target Selection Logic**
   - Brief recap of how `GroupRef` is resolved into member BMCs and how each member is dispatched.

2. **Exact Verified Curl Command**
   - Example request using a valid groupRef.
   - Expected response and job state progression.

3. **Operational Notes for Debuggers**
   - How to query job status and identify group resolution issues.
   - How to inspect per-member results in fan-out jobs.
   - How to troubleshoot lock conflicts.

4. **SMD Integration Details**
   - Exact SMD endpoints called and contract assumptions verified.
   - Error responses from SMD and how they are mapped to job failure states.
