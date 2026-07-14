# Device Profile System Handoff

## 1. Implementation Summary

The Device Profile system captures per-vendor Redfish behavior (firmware update
dispatch, device identity discovery, and firmware-inventory expansion) as a
Fabrica resource so the service can support heterogeneous BMC fleets without
hard-coded vendor logic.

Components implemented:

- **Resource type** — `apis/hardware.fabrica.dev/v1/deviceprofile_types.go`
  defines `DeviceProfile`, `DeviceProfileSpec`, `DeviceProfileStatus`,
  `VerificationSpec`, and a `Validate(ctx)` method. Stored in the existing
  generic Fabrica `Resource` table; no new Ent schema was required.
- **Registry** — `pkg/deviceProfiles/registry.go` is a thread-safe in-memory
  store of `v1.DeviceProfile` keyed by `Spec.ProfileID`, with a process-wide
  `Global` singleton and `Register`/`Get`/`List`/`Delete`/`Upsert` methods.
- **Loader** — `pkg/deviceProfiles/loader.go` parses `*.yaml`/`*.yml` files into
  profiles, applies defaults (`UpdateMethod=POST`,
  `FirmwareInventoryExpandParam="?$expand=."` when expand is enabled), records
  `Status.SourceFile`/`Status.LoadedAt`, validates, and registers each profile.
- **Matcher** — `pkg/deviceProfiles/matcher.go` provides `MatchDevice`
  (probe + regex verification), `ReadDeviceIdentity` (manufacturer/model), and
  `BuildUpdatePayload` (`%placeholder%` substitution + JSON validation). It uses
  a shared HTTP client that tolerates BMC self-signed certificates.
- **REST API** — `cmd/server/deviceprofile_routes.go` registers the
  `/deviceprofiles` CRUD + reload endpoints; `cmd/server/deviceprofile_sync.go`
  loads profiles at startup. OpenAPI paths are added in
  `cmd/server/openapi_extensions.go`.
- **Server wiring** — `cmd/server/main.go` adds the `--device-profiles-dir`
  flag (default `./device-profiles`), calls `loadDeviceProfiles(...)` during
  startup, and calls `RegisterDeviceProfileRoutes(r)` alongside the other route
  registrations.
- **Example profiles** — `device-profiles/crayex.yaml` and
  `device-profiles/ilo.yaml`.

Unit tests accompany each package:
`registry_test.go`, `loader_test.go`, `matcher_test.go` (uses an httptest TLS
server to exercise the probe/identity paths), and `deviceprofile_types_test.go`.
All pass via `go test ./pkg/deviceProfiles/... ./apis/hardware.fabrica.dev/v1/...`.

## 2. Configuration and Startup

- Flag: `--device-profiles-dir` (default `./device-profiles`).
- At startup the server scans the directory, loads every `.yaml`/`.yml` file
  into `deviceProfiles.Global`, and logs a summary. Load errors (bad YAML,
  failed validation, duplicate `id`) are logged as warnings and are non-fatal —
  a single malformed profile does not block startup or the other profiles.
- The FirmwareInventory collection URI is **never** hard-coded; profiles only
  declare whether expand is supported and which expand parameter to use. The URI
  itself is discovered at runtime via
  `GET /redfish/v1/ → UpdateService["@odata.id"] → FirmwareInventory["@odata.id"]`.

## 3. Adding a New Device Profile

### 3.1 Via YAML file (preferred for defaults shipped with the service)

1. Create `device-profiles/<vendor>.yaml`. Minimum required fields:
   `id`, `updateActionURI`, `updatePayloadTemplate`, `manufacturerPath`,
   `manufacturerField`, `modelPath`, `modelField`.
2. Add a `verification` block (`path`, `field`, `pattern`) so the matcher can
   select the profile for a given device.
3. Restart the server, or call `POST /deviceprofiles/reload` to rescan without a
   restart.

Example (`device-profiles/ilo.yaml`):

```yaml
id: ilo
name: "HPE iLO 5/6"
enabled: true
updateActionURI: /redfish/v1/UpdateService/Actions/SimpleUpdate
updatePayloadTemplate: |
  {
    "ImageURI": "%imageURI%"
  }
updateMethod: POST
manufacturerPath: /redfish/v1/Chassis/1
manufacturerField: Manufacturer
modelPath: /redfish/v1/Chassis/1
modelField: Model
supportsInventoryExpand: true
firmwareInventoryExpandParam: "?$expand=."
verification:
  path: /redfish/v1/Managers/1
  field: Model
  pattern: "^iLO"
```

### 3.2 Via the API (for runtime additions)

`POST /deviceprofiles` with a full `DeviceProfile` JSON body (see §4.3).
Profiles added this way live in the in-memory registry only; re-run the reload
endpoint or restart to reconcile with the on-disk set.

## 4. Verified curl Commands

Replace `127.0.0.1:8080` with your host/port.

### 4.1 List all profiles

```bash
curl -sS http://127.0.0.1:8080/deviceprofiles
```

Response:

```json
{
  "items": [ { "apiVersion": "hardware.fabrica.dev/v1", "kind": "DeviceProfile", "spec": { "profileID": "crayex", ... } } ],
  "count": 2
}
```

### 4.2 Get one profile

```bash
curl -sS http://127.0.0.1:8080/deviceprofiles/crayex
```

Returns the single `DeviceProfile` object, or `404` if the id is unknown.

### 4.3 Create a profile

```bash
curl -sS -X POST http://127.0.0.1:8080/deviceprofiles \
  -H 'Content-Type: application/json' \
  -d '{
    "spec": {
      "profileID": "supermicro",
      "name": "Supermicro X13",
      "enabled": true,
      "updateActionURI": "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate",
      "updatePayloadTemplate": "{\"ImageURI\": \"%imageURI%\"}",
      "manufacturerPath": "/redfish/v1/Chassis/1",
      "manufacturerField": "Manufacturer",
      "modelPath": "/redfish/v1/Chassis/1",
      "modelField": "Model",
      "supportsInventoryExpand": true,
      "firmwareInventoryExpandParam": "?$expand=*",
      "verification": { "path": "/redfish/v1/Managers/1", "field": "Manufacturer", "pattern": "^Supermicro" }
    }
  }'
```

Returns `201 Created` with the stored object, or `409 Conflict` if `profileID`
already exists, or `400 Bad Request` on validation failure.

### 4.4 Replace a profile

```bash
curl -sS -X PUT http://127.0.0.1:8080/deviceprofiles/supermicro \
  -H 'Content-Type: application/json' \
  -d '{ "spec": { "profileID": "supermicro", "enabled": false, "updateActionURI": "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", "updatePayloadTemplate": "{\"ImageURI\": \"%imageURI%\"}", "manufacturerPath": "/redfish/v1/Chassis/1", "manufacturerField": "Manufacturer", "modelPath": "/redfish/v1/Chassis/1", "modelField": "Model" } }'
```

The URL `id` is authoritative and overrides any `profileID` in the body.

### 4.5 Patch a profile

```bash
curl -sS -X PATCH http://127.0.0.1:8080/deviceprofiles/supermicro \
  -H 'Content-Type: application/json' \
  -d '{ "spec": { "enabled": true } }'
```

Supplied fields are merged onto the existing profile; the `id` cannot change.

### 4.6 Delete a profile

```bash
curl -sS -X DELETE http://127.0.0.1:8080/deviceprofiles/supermicro -i
```

Returns `204 No Content`, or `404` if the id is unknown.

### 4.7 Reload profiles from disk

```bash
curl -sS -X POST http://127.0.0.1:8080/deviceprofiles/reload
```

Response:

```json
{ "loaded": 2, "errors": [] }
```

## 5. How the Reconciler Uses a Profile

The intended dispatch flow (see plan §9) is:

1. **Profile selection** — before resolving firmware, call
   `deviceProfiles.MatchDevice(ctx, targetAddress, username, password, deviceProfiles.Global)`.
   The matcher iterates enabled profiles, GETs each profile's
   `verification.path`, extracts `verification.field` (dot-notation supported for
   nested JSON such as `Status.Health`), and returns the first profile whose
   `verification.pattern` matches. If none match it returns `ErrNoMatch`; the job
   should transition to `Failed` with `ErrorDetail = "no matching device profile"`.
2. **Device identity (optional)** — `deviceProfiles.ReadDeviceIdentity(ctx, ...)`
   reads manufacturer and model for logging/discovery.
3. **Payload build** — assemble a substitution map and call
   `deviceProfiles.BuildUpdatePayload(profile, subs)`:
   - `imageURI` → firmware proxy URI
   - `target` → Redfish target URI (one request per target for multi-target)
   - `component` → `res.Spec.Component`
   - `applyTime` → job override or profile default
   The function substitutes each `%key%` token and re-validates that the result
   parses as JSON before returning it.
4. **Dispatch** — POST (or `profile.Spec.UpdateMethod`) the payload to
   `profile.Spec.UpdateActionURI`.
5. **Status** — record the selected profile id (plan proposes a
   `FirmwareUpdateJobStatus.DeviceProfileID` field).

> Note: steps 1–5 describe the wiring point. The registry, loader, matcher, API,
> and startup loading are implemented and tested; the actual edit to the
> reconciler's `dispatchRedfishOnce` (plan §9, step 12) and the
> `FirmwareUpdateJobStatus.DeviceProfileID` field (step 11) are the remaining
> integration into the update path.

## 6. Known Limitations and Edge Cases

- **Persistence** — API-created profiles live only in the in-memory registry.
  They are not written back to the database, so they do not survive a restart;
  file-based profiles are reloaded from disk each start. Wire the generated
  Fabrica storage functions if durable API-created profiles are needed.
- **Reload semantics** — `POST /deviceprofiles/reload` upserts the freshly
  scanned file set into the global registry. It does not delete profiles that
  were removed from disk or added via the API; it is additive/replacing by id.
- **`enabled` default** — because YAML booleans default to `false`, a profile
  file that omits `enabled` is treated as disabled. Set `enabled: true`
  explicitly in profile files (both shipped examples do).
- **TLS verification** — the matcher's HTTP client uses
  `InsecureSkipVerify: true` to accommodate BMC self-signed certificates,
  matching existing Redfish access patterns in the service.
- **Template validation** — `BuildUpdatePayload` validates JSON only after
  substitution. A template that is only valid once a numeric placeholder is
  filled will fail validation if that substitution is missing at call time.
- **Cray EX expand** — Cray EX does not support OData `$expand`; its example
  profile sets `supportsInventoryExpand: false` and inventory members must be
  read individually.

## 7. Test and Build Commands

```bash
go build ./...
go test ./pkg/deviceProfiles/... ./apis/hardware.fabrica.dev/v1/...
```

Both complete successfully with the current implementation.
