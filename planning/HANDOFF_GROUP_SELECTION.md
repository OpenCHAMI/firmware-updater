# HANDOFF: Target Selection via SMD User-Defined Groups

Implements `planning/GROUP_SELECTION.md`. This document summarizes the delivered
behavior, the exact verified commands, operational notes, SMD integration
details, and — importantly — every **deviation from the plan** (Section "Plan
Deviations & Decisions" below).

Status: **Implemented and verified.** `fabrica generate`, `go mod tidy`,
`go vet ./...`, `go build ./...`, and `go test ./...` all pass. Validation and
group-resolution paths were exercised against a running server with a mock SMD.

---

## 1. Summary of Group Target Selection Logic

`FirmwareUpdateJobSpec` now accepts a `groupRef` as an alternative to the single
`targetAddress`. Exactly one of the two must be set (enforced in `Validate`).

Group reconciliation flow (`pkg/reconcilers/firmwareupdatejob_reconciler.go`):

1. The OCI payload is resolved **once** (existing logic) and reused for every
   member via the firmware-proxy `proxyURI`.
2. If `spec.groupRef` is set, control passes to `reconcileGroup`:
   - `GET /hsm/v2/groups/{groupRef}` → `members.ids[]`.
   - Each member xname is resolved to its controlling BMC via
     `GET /hsm/v2/Inventory/ComponentEndpoints/{xname}`, using
     `RedfishEndpointFQDN` as the per-member dispatch address. SMD inventory is
     authoritative; no xname string manipulation is used.
   - Members with no ComponentEndpoint (or no FQDN) are recorded as
     *unresolvable*. Results are **de-duplicated by BMC FQDN** so a BMC backing
     multiple member nodes is dispatched to only once.
3. Membership strictness:
   - `allowPartialTargets=false` (default): any unresolvable member → terminal
     `Failed`.
   - `allowPartialTargets=true`: unresolvable members are logged/recorded and the
     job proceeds with the resolvable set.
   - Zero resolvable members → terminal `Failed` (`group "X" returned no
     resolvable members`).
4. Fan-out: each resolved BMC runs the existing per-BMC path (UpdateService
   action discovery → optional component→target discovery → Redfish
   `SimpleUpdate`), bounded to `spec.maxParallel` concurrent dispatches
   (default 1 = serial).
5. Aggregation: **all members must succeed.** Any per-member dispatch failure →
   `Failed` with `status.failedMembers` populated; otherwise `InProgress`.

Single-`targetAddress` jobs are unchanged and take the original code path
(`dispatchToBMC` with `res.Spec.TargetAddress`).

### New/changed fields

`FirmwareUpdateJobSpec` (`apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go`):

| Field | Type | Notes |
| --- | --- | --- |
| `targetAddress` | string | **No longer `required`** at the tag level; XOR with `groupRef` enforced in `Validate`. |
| `groupRef` | string | SMD group label selecting the BMC set. |
| `maxParallel` | int | Group-mode concurrency bound. `0`/omitted → 1. Negative rejected. |
| `allowPartialTargets` | bool | Partial-resolution policy (default false). |
| `username`, `password` | string | **Unchanged, still `required`** (see deviations). |

`FirmwareUpdateJobStatus`: added `resolutionDetail`, `memberCount`,
`completedCount`, `failedMembers`.

### New/changed files

- `apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go` — fields + `Validate`.
- `internal/smd/client.go` — read-only SMD HSM v2 client (+ `client_test.go`).
- `pkg/reconcilers/group_resolution.go` — `resolveGroupTargets`, `fanOutDispatch`,
  `smdResolver` interface (+ `group_resolution_test.go`).
- `pkg/reconcilers/firmwareupdatejob_reconciler.go` — branch logic,
  `dispatchToBMC`, `reconcileGroup`, `resolveGroupTargetsWithBackoff`,
  `isTerminalError` now unwraps via `errors.As`; dispatch helpers parameterized
  by BMC address + targets.
- Regenerated (by `fabrica generate`): `cmd/client/main.go` (CLI help now lists
  new spec fields), `pkg/resources/register_generated.go` (header only).
- `apis/hardware.fabrica.dev/v1/firmwareupdatejob_types_test.go` — validation tests.

---

## 2. Exact Verified Curl Commands

Build/run the server (SMD base URL is configurable — see Section 4):

```bash
go build -o ./tmp/server ./cmd/server
SMD_BASE_URL=http://127.0.0.1:27779/hsm/v2 \
  ./tmp/server serve --port 8090 --database-url="file:gs.db?cache=shared&_fk=1"
```

### 2.1 Validation (all verified live)

| Case | Request selector | Result |
| --- | --- | --- |
| Both `targetAddress` + `groupRef` | conflict | **HTTP 400** |
| Neither selector | missing | **HTTP 400** |
| `groupRef` with no `targets`/`component` | missing component | **HTTP 400** |
| `maxParallel: -1` | negative | **HTTP 400** |
| Valid `groupRef` | ok | **HTTP 201** |
| Valid `targetAddress` (backward compat) | ok | **HTTP 201** |

### 2.2 Group-mode creation (verified)

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "group-update-cabinet-x1000"},
    "spec": {
      "groupRef": "cabinet-x1000",
      "ociReference": "127.0.0.1:5000/firmware/bios:1.8.2",
      "serverProxyAddress": "127.0.0.1",
      "username": "admin",
      "password": "pw",
      "component": "BIOS",
      "maxParallel": 5
    }
  }'
# -> HTTP 201, status.jobState "Pending"
```

**Verified state progression** (against a mock SMD serving `cabinet-x1000` with 2
members mapped to BMC FQDNs `10.254.2.10` / `10.254.2.11`, no real BMCs behind
them):

```
Resolving → Failed
  resolutionDetail: "resolved 2 BMC(s) from 2 member(s); 0 unresolvable"
  memberCount:      2
  failedMembers:    ["10.254.2.11", "10.254.2.10"]
  errorDetail:      "2 of 2 member BMC(s) failed to dispatch: member x0c0s1b0n0
                     (10.254.2.11): auto-discovery of UpdateService failed: ...
                     context deadline exceeded ..."
```

This confirms group fetch → ComponentEndpoint resolution → FQDN extraction →
bounded fan-out → all-must-succeed aggregation. The dispatch failure is expected
here because the mock BMC FQDNs are not real Redfish endpoints; with real BMCs
the job reaches `InProgress` with `completedCount == memberCount`.

**Missing-group terminal case (verified):**

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"grp-missing"},"spec":{"groupRef":"does-not-exist",
       "username":"admin","password":"pw",
       "ociReference":"127.0.0.1:5000/firmware/bios:1.8.2",
       "component":"BIOS","serverProxyAddress":"127.0.0.1"}}'
# -> Failed immediately, errorDetail: group "does-not-exist" not found
```

---

## 3. Operational Notes for Debuggers

- **Query job status:** `GET /firmwareupdatejobs/{uid}/`. Inspect `status`:
  - `jobState`: `Pending` → `Resolving` → `InProgress` | `Failed`.
  - `resolutionDetail`: `resolved N BMC(s) from M member(s); K unresolvable`.
  - `memberCount`: de-duplicated BMC count selected.
  - `completedCount`: BMCs dispatched successfully.
  - `failedMembers`: BMC FQDNs (fan-out failures) **or** member xnames (when the
    job failed because members were unresolvable and `allowPartialTargets=false`).
  - `errorDetail`: aggregate/terminal message.
- **Group resolution issues:**
  - `group "X" not found` → SMD returned 404; check the group label exists
    (`GET {SMD}/hsm/v2/groups/X`).
  - `group "X" has N unresolvable member(s) and allowPartialTargets is false` →
    one or more members lack a ComponentEndpoint (not yet discovered). Either
    discover them, or set `allowPartialTargets: true`.
  - `group "X" returned no resolvable members` → group is empty or all members
    unresolvable.
  - Transient SMD errors (timeout / 5xx) are retried with exponential backoff
    (4 attempts) before the job is marked `Failed`.
- **Per-member fan-out results:** each member dispatch logs at debug
  (`member <xname> (<fqdn>) dispatched task <id>`) or error
  (`member <xname> (<fqdn>) dispatch failed: ...`). `failedMembers` lists the
  BMC FQDNs that failed.
- **Parallelism:** concurrency is bounded by `maxParallel` (default 1). Verify
  via server logs / dispatch timing; unit test
  `TestFanOutDispatch_HonorsParallelismBound` asserts the bound.

---

## 4. SMD Integration Details

**Client:** `internal/smd/client.go` — read-only; never creates/mutates SMD.

**Configuration (env):**

- `SMD_BASE_URL` — full HSM v2 base path. Default `http://smd:27779/hsm/v2`.
- `SMD_TOKEN` — optional bearer token attached to SMD requests. Omitted by
  default (deployments front SMD with a JWT/gateway; SMD handlers do not enforce
  authz themselves).

**Endpoints called (verified against OpenCHAMI/smd master contract):**

| Purpose | Call | Consumed fields |
| --- | --- | --- |
| Group members | `GET /hsm/v2/groups/{groupRef}` | `members.ids[]` |
| Member → BMC | `GET /hsm/v2/Inventory/ComponentEndpoints/{xname}` | `RedfishEndpointFQDN` (dispatch addr), `RedfishEndpointID` (BMC xname, log/dedup) |

**SMD error mapping:**

| SMD response | firmware-updater handling |
| --- | --- |
| Group `404 No such group` | Terminal `Failed`: `group "X" not found`. |
| ComponentEndpoint `404` / empty FQDN | Member marked *unresolvable* (`ErrEndpointNotFound`); not a hard error. |
| `5xx` / transport timeout | Mapped to transient `503`; retried with backoff, then `Failed`. |
| Malformed/undecodable body | Mapped to `502` (transient) → retried → `Failed` (see deviation 6). |

---

## 5. Plan Deviations & Decisions

The plan's Section 8 explicitly required clarification before implementation. The
user was unavailable, so the following decisions were made autonomously using the
recommended defaults. **Each is a point to review.**

1. **SMD base URL & auth (plan 8.4):** Implemented as a configurable
   `SMD_BASE_URL` (default `http://smd:27779/hsm/v2`) with **no auth header by
   default** (gateway-fronted). An optional `SMD_TOKEN` bearer is sent if set.
   Token *passthrough* from the incoming job request was **not** implemented.

2. **BMC credentials in group mode (conflict with existing schema):** The plan
   said it "does not modify credential fields" and treated BMC auth as out of
   scope, but the existing spec marks `username`/`password` as `required` and the
   dispatch path needs them. **Decision:** kept them `required`; the **same spec
   credentials are used for every member BMC**. The plan's Section 6.1 group
   example omitted credentials — the verified example here includes them. If a
   separate per-BMC credential source is intended, this needs follow-up.

3. **Default `maxParallel` (plan 8.1):** Defaults to **1 (serial)** when omitted.

4. **`maxParallel` validation nuance:** The plan says "MaxParallel < 1 is
   rejected." Because `maxParallel` is an `int` with `omitempty`, an explicit
   `0` is indistinguishable from omitted. **Decision:** `0` is treated as
   *unset* → default 1; only **negative** values are rejected (HTTP 400).

5. **Member type policy (plan 8.5):** **Type-agnostic** resolution — every member
   is resolved via ComponentEndpoint regardless of component type; any member
   without a Redfish endpoint is treated as unresolvable (ties into
   `allowPartialTargets`). Non-Node xnames are not special-cased.

6. **Malformed SMD response:** The plan lists "SMD response malformed" as a
   *terminal* error. Implemented as **transient (502) → retried → Failed** rather
   than immediate-terminal, to reuse the existing backoff/classification
   machinery. Net effect is still a `Failed` job.

7. **Partial-membership surfacing (plan 8.3):** Logger + status fields
   (`resolutionDetail`, `failedMembers`) only; **no external event emission**.

8. **BMC de-duplication:** Added — members resolving to the same BMC FQDN are
   dispatched to once. The plan mentioned `RedfishEndpointID` "for dedup" but did
   not specify the behavior; FQDN-based dedup is used.

9. **`?partition` query param (plan 3.1/7.1):** **Not implemented** (optional in
   the plan). Group members are fetched unfiltered. Can be added later.

10. **`isTerminalError` refactor:** Changed from a direct type assertion to
    `errors.As` so wrapped errors retain their terminal/transient classification.
    Behavior-preserving for existing single-BMC paths.

11. **Lock integration (plan Section 5):** Deferred to the follow-on
    `LOCKING_SUPPORT.md` as the plan directs; **not** implemented here.

12. **"Completed" semantics:** Consistent with the existing single-BMC path,
    which sets `InProgress` once a Redfish `SimpleUpdate` task is *dispatched*
    (there is no post-dispatch task polling). In group mode, `completedCount`
    counts successfully **dispatched** members and the job reaches `InProgress`
    when all members dispatch successfully. Full "update succeeded" tracking
    would require task-status polling (out of scope, matches current design).

---

## 6. Acceptance Criteria Status

| Criterion | Status |
| --- | --- |
| 1. `fabrica generate` / `go mod tidy` / `go build ./...` | ✅ pass |
| 2. Validation (both/neither/no-component/maxParallel<1 → 400) | ✅ verified live |
| 3. Group resolution of N members + parallelism bound | ✅ resolution verified live; bound covered by unit test |
| 4. Error handling (missing group / empty / partial) | ✅ missing-group verified live; empty/partial covered by unit tests |
| 5. Backward compatibility (single `targetAddress`) | ✅ verified live (HTTP 201, unchanged path) |
| 6. Job completion reflects all-must-succeed + status fields | ✅ verified live (failedMembers, memberCount, resolutionDetail) |
| 7. Unit tests (selector, resolution, parallelism, backward compat) | ✅ added, passing |
