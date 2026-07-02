# HANDOFF_LOCKING_SUPPORT

## 1) Implemented Lock Gate Behavior

This phase is implemented in the reconcile path before Redfish dispatch.

Flow now is:
1. Resolve payload digest/version from OCI reference or discovery.
2. Discover Redfish `SimpleUpdate` action URI.
3. Resolve component-based `Targets` from FirmwareInventory when needed.
4. Query SMD lock status for in-scope component IDs.
5. If conflicts exist:
   - `spec.ignoreLocks=false` -> mark job `Failed` and stop dispatch.
   - `spec.ignoreLocks=true` -> record conflicts and continue.
6. Dispatch firmware update to Redfish and mark `InProgress` on success.

Implemented files:
- `apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go`
  - Added `spec.ignoreLocks` (bool, optional).
  - Added `status.lockConflicts` ([]string, optional).
- `pkg/reconcilers/firmwareupdatejob_reconciler.go`
  - Added pre-dispatch lock gate call.
  - Clears `status.lockConflicts` at reconcile start.
- `pkg/reconcilers/firmwareupdatejob_locking.go`
  - New SMD lock-status client and conflict evaluator.
  - Uses `POST /hsm/v2/locks/status` with `ComponentIDs` body.
  - Treats both `Locked` and `Reserved` as conflicts.
  - Includes `NotFound[]` IDs in `status.lockConflicts`.
- `pkg/reconcilers/firmwareupdatejob_locking_test.go`
  - Added lock behavior tests.

## 2) Example Requests

### A) Job blocked by lock conflict (`ignoreLocks=false`)

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": { "name": "lock-blocked-job" },
    "spec": {
      "targetAddress": "x3000c0s1b0",
      "username": "root",
      "password": "initial0",
      "serverProxyAddress": "10.254.1.20",
      "component": "BMC",
      "ignoreLocks": false,
      "discovery": {
        "repository": "127.0.0.1:5000/firmware/cray-bmc",
        "hardwareModel": "x3000",
        "version": "latest"
      }
    }
  }'
```

Expected behavior:
- If SMD reports target as `Locked` or `Reserved`, job is set to `Failed`.
- `status.lockConflicts` includes detailed entries.

### B) Job proceeds despite lock conflict (`ignoreLocks=true`)

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": { "name": "lock-override-job" },
    "spec": {
      "targetAddress": "x3000c0s1b0",
      "username": "root",
      "password": "initial0",
      "serverProxyAddress": "10.254.1.20",
      "component": "BMC",
      "ignoreLocks": true,
      "discovery": {
        "repository": "127.0.0.1:5000/firmware/cray-bmc",
        "hardwareModel": "x3000",
        "version": "latest"
      }
    }
  }'
```

Expected behavior:
- Conflicts are still written to `status.lockConflicts`.
- Reconcile continues to Redfish dispatch.

## 3) Troubleshooting Lock Conflicts

1. Read `status.lockConflicts` on the job:
   - Entries include `component`, `locked`, `reserved`, `creationTime`, `expirationTime`.
   - Missing SMD records are surfaced as `component=<id> notFound=true`.

2. Query SMD lock status directly:

```bash
curl -sS -X POST http://<smd-host>:27779/hsm/v2/locks/status \
  -H 'Content-Type: application/json' \
  -d '{"ComponentIDs":["x3000c0s1b0"]}'
```

3. Check firmware-updater SMD integration environment:
   - `FIRMWARE_UPDATER_SMD_BASE_URL` (default: `http://localhost:27779`)
   - `FIRMWARE_UPDATER_SMD_TOKEN` (optional bearer token)

## 4) SMD Integration Contract Used

Implemented contract:
- Endpoint: `POST /hsm/v2/locks/status`
- Request body: `{ "ComponentIDs": ["xname1", "xname2"] }`
- Response fields consumed:
  - `Components[].ID`
  - `Components[].Locked`
  - `Components[].Reserved`
  - `Components[].CreationTime`
  - `Components[].ExpirationTime`
  - `NotFound[]`

Policy implemented:
- Conflict condition: `Locked == true OR Reserved == true`.
- `NotFound[]` is surfaced in `status.lockConflicts` for operator triage.
- Conflict is terminal only when `ignoreLocks=false`.

## 5) Validation Run

Validation executed successfully:
- `go test ./...`
- `go build ./...`

Note:
- `fabrica generate` currently fails in this workspace due to missing embedded template `templates\\server\\routes.go.tmpl` in the Fabrica toolchain. This is external to the lock-gate code changes.
