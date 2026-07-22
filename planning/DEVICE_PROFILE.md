# Device Profile System

## 1. Background and Motivation

The prior `DEVICE_PROFILE.md` plan described profiles compiled directly into the codebase.
This plan replaces that approach with a fully runtime-driven system:

- Device profiles are stored in YAML config files on disk.
- At startup, all profile files are read into an in-memory registry.
- Each profile is also persisted to the SQLite database through the standard Fabrica storage layer.
- REST API endpoints allow operators to inspect, add, modify, and remove profiles without
  restarting the service.
- The reconciler consults the registry to select the correct profile for a target device,
  then uses that profile to determine where to find manufacturer/model in the Redfish tree and
  which update command and payload template to send.

**Fabrica compliance requirement:**
`DeviceProfile` is a first-class Fabrica resource.  It is added to `apis.yaml`, scaffolded
with `fabrica add resource DeviceProfile`, and follows the same Kubernetes-style
`APIVersion / Kind / Metadata / Spec / Status` envelope used by `FirmwareUpdateJob` and
`FirmwareUpdateCampaign`.  Standard Fabrica-generated handlers, routes, and storage adapter
functions provide the CRUD API.  Only non-CRUD endpoints (`/reload`) are added as custom
routes in `openapi_extensions.go`.

---

## 2. Redfish Firmware Update Command and Payload Survey

Different BMC vendors implement the Redfish UpdateService in incompatible ways.
The table below summarizes the known variants the profile system must accommodate.

| Vendor / Platform         | Update Action URI                                                              | Required Payload Fields                                                              | Notes                                                   |
|---------------------------|--------------------------------------------------------------------------------|--------------------------------------------------------------------------------------|---------------------------------------------------------|
| Standard (DMTF)           | `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`                 | `ImageURI`, `TransferProtocol`, `Targets[]`                                          | Baseline; used by most modern BMCs                      |
| HPE iLO 5 / iLO 6        | `/redfish/v1/UpdateService/Actions/SimpleUpdate`                               | `ImageURI`                                                                           | Uses shorter action name; `Targets` ignored             |
| HPE Cray EX (liquid-cooled)| `/redfish/v1/UpdateService/Actions/SimpleUpdate`                | `ImageURI`, `TransferProtocol`, `Targets[]`                                          | Targets must reference FirmwareInventory URIs           |
| Dell iDRAC                | `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`                 | `ImageURI`, `TransferProtocol`, `Targets[]`, optionally `ApplyTime`                  | Creates a Redfish Job; task URI returned in `Location`  |
| Dell iDRAC (multipart)    | `POST /redfish/v1/UpdateService/upload`                                        | Multipart form with `file` part                                                      | Alternative path for direct binary push                 |
| Lenovo XCC                | `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`                 | `ImageURI`, `TransferProtocol`, optionally `Targets[]`                               | Targets may be empty; XCC selects component itself      |
| AMI MegaRAC               | `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`                 | `ImageURI`, `TransferProtocol`                                                       | No `Targets` field supported                            |
| OpenBMC                   | `/redfish/v1/UpdateService/Actions/UpdateService.StartUpdate` (if supported)  | Upload to `/redfish/v1/UpdateService` via multipart, then start                      | Two-step process on some builds                         |
| Supermicro BMC            | `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`                 | `ImageURI`, `TransferProtocol`, `Targets[]`                                          | May require `OemRecovery` extension field               |

Payload placeholders used in template strings (resolved at dispatch time):

| Placeholder       | Resolved To                                                           |
|-------------------|-----------------------------------------------------------------------|
| `%imageURI%`      | Full proxy URI built from `serverProxyAddress` and the payload digest |
| `%target%`        | A single Redfish FirmwareInventory URI from the resolved target list  |
| `%component%`     | The component string from the job spec                                |
| `%applyTime%`     | Literal from profile or job spec override (`Immediate`, `OnReset`, …) |

### FirmwareInventory Path Discovery

The DMTF specification treats all URIs below the service root as opaque — clients must
not hard-code them.  The `FirmwareInventory` collection URI **must always be discovered
at runtime** by following hypermedia links:

```
GET /redfish/v1/
  → body["UpdateService"]["@odata.id"]   (e.g. /redfish/v1/UpdateService)
GET <UpdateService URI>
  → body["FirmwareInventory"]["@odata.id"] (e.g. /redfish/v1/UpdateService/FirmwareInventory)
```

The collection root is `/redfish/v1/UpdateService/FirmwareInventory` on all major modern
platforms (iLO, iDRAC, Cray EX, XCC, OpenBMC), but this should still be discovered
dynamically to remain compatible with non-standard implementations.

### FirmwareInventory OData Expand Parameter Variance

When `supportsInventoryExpand` is true, the expand query string appended to the collection
URI differs across vendors:

| Vendor / Platform   | Expand Parameter            | Notes                                                           |
|---------------------|-----------------------------|-----------------------------------------------------------------|
| Standard (DMTF)     | `?$expand=.`                | OData 4.0 standard; default                                     |
| HPE iLO 5 / iLO 6  | `?$expand=.`                |                                                                 |
| HPE Cray EX         | Not confirmed               | HPE's own FAS tool reads member URIs individually; set `supportsInventoryExpand: false` |
| Dell iDRAC          | `?$expand=*($levels=1)`     | Uses OData levels syntax                                        |
| Lenovo XCC          | `?$expand=.`                |                                                                 |
| AMI MegaRAC         | Not supported               | Set `supportsInventoryExpand: false`                            |
| OpenBMC             | `?$expand=.`                | Support varies by build                                         |
| Supermicro BMC      | `?$expand=*`                | No levels qualifier                                             |

---

## 3. Device Identity Location Survey

To look up manufacturer and model name, the service must know which Redfish path to read
and which JSON fields inside that response contain the values.
These differ across vendors.

| Vendor / Platform     | Manufacturer Path                                       | Manufacturer Field | Model Path                                              | Model Field |
|-----------------------|---------------------------------------------------------|--------------------|---------------------------------------------------------|-------------|
| HPE iLO 5 / iLO 6    | `/redfish/v1/Chassis/1`                                 | `Manufacturer`     | `/redfish/v1/Chassis/1`                                 | `Model`     |
| HPE Cray EX           | `/redfish/v1/Chassis/Enclosure`                         | `Manufacturer`     | `/redfish/v1/Chassis/Enclosure`                         | `Model`     |
| Dell iDRAC            | `/redfish/v1/Systems/System.Embedded.1`                 | `Manufacturer`     | `/redfish/v1/Systems/System.Embedded.1`                 | `Model`     |
| Lenovo XCC            | `/redfish/v1/Systems/1`                                 | `Manufacturer`     | `/redfish/v1/Systems/1`                                 | `Model`     |
| Standard Systems/1    | `/redfish/v1/Systems/1`                                 | `Manufacturer`     | `/redfish/v1/Systems/1`                                 | `Model`     |
| OpenBMC               | `/redfish/v1/Systems/system`                            | `Manufacturer`     | `/redfish/v1/Systems/system`                            | `Model`     |
| Supermicro BMC        | `/redfish/v1/Systems/1`                                 | `Manufacturer`     | `/redfish/v1/Systems/1`                                 | `Model`     |
| Fallback (root)       | `/redfish/v1/`                                          | `Vendor`           | `/redfish/v1/`                                          | `Product`   |

Profile-level verification (used to select the correct profile before reading identity) can
read any Redfish path and match a field value against a regular expression.
Common verification anchors:

| Vendor / Platform    | Verification Path                                           | Field      | Pattern             |
|----------------------|-------------------------------------------------------------|------------|---------------------|
| HPE iLO              | `/redfish/v1/Managers/1`                                    | `Model`    | `^iLO`              |
| HPE Cray EX          | `/redfish/v1/UpdateService/FirmwareInventory/BMC`           | `SoftwareId` | `^(nc\|cc\|sc)` |
| Dell iDRAC           | `/redfish/v1/Managers/iDRAC.Embedded.1`                     | `Model`    | `^iDRAC`            |
| Lenovo XCC           | `/redfish/v1/Managers/1`                                    | `Model`    | `^XCC`              |
| OpenBMC              | `/redfish/v1/`                                              | `Vendor`   | `^OpenBMC`          |

---

## 4. Device Profile Config File Format

Each profile is stored in one YAML file.
A directory (configurable, default `./device-profiles/`) is scanned at startup.
Files must have a `.yaml` or `.yml` extension.
Profile IDs must be unique across all loaded files; a duplicate ID is a fatal load error.

### 4.1 YAML Schema

```yaml
# Required. Unique identifier for this profile family.
id: crayex

# Human-readable display name.
name: "HPE Cray EX (liquid-cooled)"

# Whether this profile is eligible for automatic selection. Default: true.
enabled: true

# ----- Update dispatch -----

# Full action URI.  May be absolute or relative (relative is resolved against
# "https://<targetAddress>").
updateActionURI: /redfish/v1/UpdateService/Actions/SimpleUpdate

# JSON template for the POST body.  Use %placeholder% tokens.
# Required keys: imageURI.  Optional: targets, transferProtocol, applyTime.
updatePayloadTemplate: |
  {
    "ImageURI": "%imageURI%",
    "TransferProtocol": "HTTP",
    "Targets": ["%target%"]
  }

# HTTP method for the update request. Default: POST.
updateMethod: POST

# ----- Device identity -----

# Redfish path to GET for the manufacturer value.
manufacturerPath: /redfish/v1/Chassis/Enclosure
# JSON field name inside the response body.
manufacturerField: Manufacturer

# Redfish path to GET for the model value.
modelPath: /redfish/v1/Chassis/Enclosure
# JSON field name inside the response body.
modelField: Model

# ----- Inventory expansion -----

# If true, the device supports listing the full firmware inventory in a single
# request by appending an OData expand parameter to the FirmwareInventory URI,
# discovered at runtime from UpdateService["FirmwareInventory"]["@odata.id"].
# When enabled, one request enumerates all inventory items without reading each
# member URI individually.  When false, each member is fetched separately.
supportsInventoryExpand: false

# OData expand parameter appended to the FirmwareInventory URI when
# supportsInventoryExpand is true.  Syntax varies by vendor:
#   "?$expand=."             – OData standard (Lenovo, OpenBMC)
#   "?$expand=*($levels=1)" – Dell iDRAC
#   "?$expand=*"            – some AMI/Supermicro builds
# Omit or leave empty to use the default "?$expand=.".
firmwareInventoryExpandParam: "?$expand=."

# Note on path discovery: the FirmwareInventory collection URI is NOT
# hard-coded.  The service reads it at runtime by GETting:
#   /redfish/v1/ → UpdateService["@odata.id"] → FirmwareInventory["@odata.id"]
# Per DMTF guidance, URIs must be treated as opaque and discovered via
# hypermedia links, not assumed.  The profile does not need to store this path.

# ----- Profile selection verification -----

# verification defines how to probe a device to confirm this profile applies.
# All fields are required when verification is present.
verification:
  # Redfish path to GET.
  path: /redfish/v1/UpdateService/FirmwareInventory/BMC
  # JSON field inside the response to inspect.
  field: SoftwareId
  # Regular expression the field value must match.
  pattern: "^(nc|cc|sc)"
```

### 4.2 Minimal Example (iLO)

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

---

## 5. Go Type Definitions

### 5.1 Fabrica Resource — `apis/hardware.fabrica.dev/v1/deviceprofile_types.go`

`DeviceProfile` is defined as a Fabrica resource using the same envelope pattern as
`FirmwareUpdateJob`.  All profile fields live in `DeviceProfileSpec`.  Status carries
only metadata about how/when the profile was loaded.

```go
package v1

import (
    "context"
    "fmt"
    "regexp"
    "strings"

    "github.com/openchami/fabrica/pkg/fabrica"
)

// DeviceProfile is a Fabrica resource that captures all per-vendor Redfish
// behaviors needed to identify a device and perform a firmware update.
type DeviceProfile struct {
    APIVersion string              `json:"apiVersion"`
    Kind       string              `json:"kind"`
    Metadata   fabrica.Metadata    `json:"metadata"`
    Spec       DeviceProfileSpec   `json:"spec"   validate:"required"`
    Status     DeviceProfileStatus `json:"status,omitempty"`
}

// DeviceProfileSpec holds all profile configuration.
type DeviceProfileSpec struct {
    // ProfileID is the stable, human-readable identifier used for lookup
    // (e.g. "crayex", "ilo").  Must be unique across all loaded profiles.
    ProfileID string `json:"profileID" validate:"required" yaml:"id"`

    // Human-readable display name.
    Name string `json:"name" yaml:"name"`

    // Enabled controls whether this profile is considered during matching.
    Enabled bool `json:"enabled" yaml:"enabled"`

    // ----- Update dispatch -----

    // UpdateActionURI is the Redfish action path (absolute or relative).
    UpdateActionURI string `json:"updateActionURI" validate:"required" yaml:"updateActionURI"`

    // UpdatePayloadTemplate is a JSON template with %placeholder% tokens.
    UpdatePayloadTemplate string `json:"updatePayloadTemplate" validate:"required" yaml:"updatePayloadTemplate"`

    // UpdateMethod is the HTTP method; defaults to POST.
    UpdateMethod string `json:"updateMethod" yaml:"updateMethod"`

    // ----- Device identity -----

    ManufacturerPath  string `json:"manufacturerPath"  validate:"required" yaml:"manufacturerPath"`
    ManufacturerField string `json:"manufacturerField" validate:"required" yaml:"manufacturerField"`
    ModelPath         string `json:"modelPath"         validate:"required" yaml:"modelPath"`
    ModelField        string `json:"modelField"        validate:"required" yaml:"modelField"`

    // SupportsInventoryExpand indicates the device accepts an OData expand query
    // appended to the FirmwareInventory URI discovered from the UpdateService link.
    // When true, the service fetches inventory in a single call; when false it
    // reads each member URI individually.
    // The FirmwareInventory URI itself is always discovered at runtime via:
    //   GET /redfish/v1/ → UpdateService["@odata.id"] → FirmwareInventory["@odata.id"]
    SupportsInventoryExpand bool `json:"supportsInventoryExpand" yaml:"supportsInventoryExpand"`

    // FirmwareInventoryExpandParam is the OData query string appended to the
    // FirmwareInventory URI when SupportsInventoryExpand is true.
    // Common values:
    //   "?$expand=."             – OData standard (default)
    //   "?$expand=*($levels=1)" – Dell iDRAC
    //   "?$expand=*"            – some AMI/Supermicro builds
    // Defaults to "?$expand=." when empty.
    FirmwareInventoryExpandParam string `json:"firmwareInventoryExpandParam" yaml:"firmwareInventoryExpandParam"`

    // Verification describes how to probe a device to confirm this profile applies.
    Verification VerificationSpec `json:"verification" yaml:"verification"`
}

// VerificationSpec defines the Redfish probe used for profile selection.
type VerificationSpec struct {
    Path    string `json:"path"    yaml:"path"`
    Field   string `json:"field"   yaml:"field"`
    Pattern string `json:"pattern" yaml:"pattern"`
}

// DeviceProfileStatus records how and when a profile was loaded.
type DeviceProfileStatus struct {
    // SourceFile is the filesystem path the profile was loaded from.
    // Empty when the profile was created directly via the API.
    SourceFile string `json:"sourceFile,omitempty"`

    // LoadedAt is an RFC3339 timestamp of the most recent load/upsert.
    LoadedAt string `json:"loadedAt,omitempty"`
}

// Validate implements Fabrica's validation interface.
func (p *DeviceProfile) Validate(ctx context.Context) error {
    if strings.TrimSpace(p.Spec.ProfileID) == "" {
        return fmt.Errorf("spec.profileID is required")
    }
    if !validProfileID.MatchString(p.Spec.ProfileID) {
        return fmt.Errorf("spec.profileID must match [a-z0-9_-]+")
    }
    if !strings.HasPrefix(p.Spec.UpdateActionURI, "/") {
        return fmt.Errorf("spec.updateActionURI must start with /")
    }
    if p.Spec.Verification.Pattern != "" {
        if _, err := regexp.Compile(p.Spec.Verification.Pattern); err != nil {
            return fmt.Errorf("spec.verification.pattern is not a valid regexp: %w", err)
        }
    }
    return nil
}

var validProfileID = regexp.MustCompile(`^[a-z0-9_-]+$`)
```

### 5.2 `pkg/deviceProfiles/registry.go`

The in-memory registry holds `v1.DeviceProfile` values (the Fabrica resource type),
keyed by `Spec.ProfileID`.

```go
package deviceProfiles

import (
    "sync"
    v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
)

// Registry is a thread-safe in-memory store of loaded DeviceProfiles.
type Registry struct {
    mu       sync.RWMutex
    profiles map[string]v1.DeviceProfile // keyed by Spec.ProfileID
}

var Global = &Registry{profiles: make(map[string]v1.DeviceProfile)}

func (r *Registry) Register(p v1.DeviceProfile) error         { ... } // error on duplicate ProfileID
func (r *Registry) Get(id string) (v1.DeviceProfile, bool)    { ... }
func (r *Registry) List() []v1.DeviceProfile                  { ... }
func (r *Registry) Delete(id string) bool                     { ... }
func (r *Registry) Upsert(p v1.DeviceProfile)                 { ... }
```

### 5.3 `pkg/deviceProfiles/loader.go`

The loader parses YAML files into `v1.DeviceProfile` resources, applying defaults
(`Kind: "DeviceProfile"`, `APIVersion: "hardware.fabrica.dev/v1"`, `Spec.Enabled: true`,
`Spec.UpdateMethod: "POST"`) before validation.

```go
package deviceProfiles

import v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"

// LoadDirectory reads all *.yaml / *.yml files from dir and registers each
// parsed DeviceProfile in reg.  Returns one error per bad file; non-fatal.
func LoadDirectory(dir string, reg *Registry) []error { ... }

// LoadFile reads, parses, and validates a single profile YAML file.
func LoadFile(path string) (v1.DeviceProfile, error) { ... }
```

### 5.4 `pkg/deviceProfiles/matcher.go`

```go
package deviceProfiles

import (
    "context"
    v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
)

// MatchDevice probes the target BMC and returns the first enabled profile
// whose Verification rule matches.  Returns ErrNoMatch if none match.
func MatchDevice(ctx context.Context, targetAddress, username, password string, reg *Registry) (v1.DeviceProfile, error) { ... }

// ReadDeviceIdentity reads manufacturer and model using the paths and fields
// from the given profile's Spec.
func ReadDeviceIdentity(ctx context.Context, targetAddress, username, password string, p v1.DeviceProfile) (manufacturer, model string, err error) { ... }

// BuildUpdatePayload resolves %placeholder% tokens in Spec.UpdatePayloadTemplate.
func BuildUpdatePayload(p v1.DeviceProfile, subs map[string]string) ([]byte, error) { ... }
```

---

## 6. Database Storage

Because `DeviceProfile` is a Fabrica resource, **no new Ent schema is required**.
Fabrica stores all resources in the existing generic `Resource` Ent table, serialising
`Spec` and `Status` as JSON blobs — identical to how `FirmwareUpdateJob` is stored.

Running `fabrica add resource DeviceProfile` followed by `fabrica generate` produces
all required storage adapter functions automatically:

```go
// Generated in internal/storage/storage_generated.go (do not edit)
func LoadAllDeviceProfiles(ctx context.Context) ([]v1.DeviceProfile, error)
func LoadDeviceProfile(ctx context.Context, uid string) (*v1.DeviceProfile, error)
func SaveDeviceProfile(ctx context.Context, p *v1.DeviceProfile) error
func DeleteDeviceProfile(ctx context.Context, uid string) error
```

`DeviceProfileStatus.SourceFile` (set at load time for file-sourced profiles) provides
provenance without needing a separate DB column.

No custom `internal/storage/ent/schema/deviceprofile.go` file is needed.

---

## 7. Startup Sequence

The server startup code in `cmd/server/main.go` (or a new `cmd/server/import.go` section)
must execute these steps before accepting requests:

1. Read `--device-profiles-dir` flag (default: `./device-profiles/`).
2. Call `deviceProfiles.LoadDirectory(dir, deviceProfiles.Global)` to populate the in-memory registry.
3. For each successfully loaded profile, call `storage.UpsertDeviceProfile(ctx, p, filePath)`.
4. On startup, also call `storage.ListDeviceProfiles(ctx)` and upsert any DB-only profiles
   (added via API after last restart) back into the in-memory registry.
5. Log a summary: how many profiles loaded from files, how many recovered from DB.

Step 4 ensures profiles that were added via the API (not from files) survive a restart.

---

## 8. REST API Endpoints

Register these routes in `cmd/server/openapi_extensions.go` or a dedicated
`cmd/server/deviceprofile_routes.go`.

All routes operate on both the in-memory registry and the SQLite DB atomically
(registry first, then DB; on DB failure, roll back the registry change).

| Method   | Path                              | Description                                        |
|----------|-----------------------------------|----------------------------------------------------|
| `GET`    | `/deviceprofiles`                 | List all profiles (registry view)                  |
| `GET`    | `/deviceprofiles/{id}`            | Get a single profile by ID                         |
| `POST`   | `/deviceprofiles`                 | Register a new profile (body: DeviceProfile JSON)  |
| `PUT`    | `/deviceprofiles/{id}`            | Replace an existing profile entirely               |
| `PATCH`  | `/deviceprofiles/{id}`            | Partial update (merge supplied fields)             |
| `DELETE` | `/deviceprofiles/{id}`            | Remove profile from registry and DB                |
| `POST`   | `/deviceprofiles/reload`          | Rescan `--device-profiles-dir` and re-sync DB      |

### 8.1 Request / Response Shapes

**`GET /deviceprofiles`** response:

```json
{
  "items": [
    {
      "id": "crayex",
      "name": "HPE Cray EX",
      "enabled": true,
      "updateActionURI": "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate",
      ...
    }
  ],
  "count": 1
}
```

**`POST /deviceprofiles`** request body: a single `DeviceProfile` JSON object.
Returns `201 Created` with the stored object, or `409 Conflict` if `id` already exists.

**`PUT /deviceprofiles/{id}`** request body: a complete `DeviceProfile` JSON object.
Returns `200 OK` with the updated object.

**`PATCH /deviceprofiles/{id}`** request body: partial `DeviceProfile` (only supplied
fields are updated).  Returns `200 OK` with the full updated object.

**`DELETE /deviceprofiles/{id}`** returns `204 No Content` on success,
`404 Not Found` if the ID does not exist.

**`POST /deviceprofiles/reload`** response:

```json
{
  "loaded": 4,
  "errors": []
}
```

### 8.2 Validation Rules

- `id` must be non-empty, contain only `[a-z0-9_-]`, and be unique.
- `updateActionURI` must be a non-empty string starting with `/`.
- `updatePayloadTemplate` must be valid JSON after all `%placeholder%` tokens are replaced
  with the empty string (validate at load time).
- `verification.pattern` must compile as a Go regular expression.
- `manufacturerPath`, `manufacturerField`, `modelPath`, `modelField` must all be non-empty.

---

## 9. Reconciler Integration

When the reconciler dispatches a `FirmwareUpdateJob`:

1. **Profile selection** (new step, before existing `Resolving` state):
   - Call `deviceProfiles.MatchDevice(ctx, targetAddress, username, password, deviceProfiles.Global)`.
   - If no profile matches, set `JobState = "Failed"`, `ErrorDetail = "no matching device profile"`.
   - Store the matched profile ID in `Status.DeviceProfileID` (new status field).

2. **Device identity** (optional, for logging / discovery):
   - Call `deviceProfiles.ReadDeviceIdentity(ctx, ...)` with the matched profile.
   - Log manufacturer and model.

3. **Update dispatch** (replace hard-coded payload in `dispatchRedfishOnce`):
   - Build substitution map:
     - `imageURI` → proxy URI
     - `target` → each Redfish target URI (one request per target if multi-target)
     - `component` → `res.Spec.Component`
     - `applyTime` → profile default or job spec override
   - Call `deviceProfiles.BuildUpdatePayload(profile, subs)` to produce the request body.
   - Use `profile.UpdateActionURI` as the action URI (overrides current hard-coded
     `discoverUpdateServiceAction` result when the profile provides one).
   - Use `profile.UpdateMethod` as the HTTP method (default `POST`).

4. **`FirmwareUpdateJobStatus` changes**:
   - Add `DeviceProfileID string` field to `FirmwareUpdateJobStatus`.

---

## 10. File and Package Layout

```
apis/
  hardware.fabrica.dev/
    v1/
      deviceprofile_types.go   – DeviceProfile, DeviceProfileSpec, DeviceProfileStatus,
                                  VerificationSpec, Validate()  ← hand-written

pkg/
  deviceProfiles/
    registry.go       – thread-safe Registry holding v1.DeviceProfile; Global singleton
    loader.go         – LoadDirectory, LoadFile  (YAML → v1.DeviceProfile)
    matcher.go        – MatchDevice, ReadDeviceIdentity, BuildUpdatePayload
    registry_test.go  – unit tests: register, get, list, delete, upsert
    loader_test.go    – unit tests: valid YAML, missing fields, bad regex, duplicate ID
    matcher_test.go   – unit tests: placeholder substitution, regex match, no-match path

cmd/
  server/
    deviceprofile_handlers_generated.go  – ⚡ generated by `fabrica generate`
    deviceprofile_sync.go                – hand-written: hooks generated handlers to
                                           keep deviceProfiles.Global in sync with DB
    openapi_extensions.go                – add /deviceprofiles/reload custom route
                                           and registerCustomOpenAPIPaths entry
    routes_generated.go                  – ⚡ updated by `fabrica generate` (adds
                                           DeviceProfile CRUD routes automatically)

apis.yaml                                – add DeviceProfile to resources list

device-profiles/                         – default config directory (committed examples)
  crayex.yaml
  ilo.yaml
```

No new Ent schema file is needed; `DeviceProfile` is stored in the existing generic
`Resource` table, the same as every other Fabrica resource.

---

## 11. CLI Flags

Add to `cmd/server/main.go`:

| Flag                     | Default              | Description                                   |
|--------------------------|----------------------|-----------------------------------------------|
| `--device-profiles-dir`  | `./device-profiles/` | Directory to scan for `.yaml`/`.yml` profiles |

---

## 12. Implementation Order

1. **`apis/hardware.fabrica.dev/v1/deviceprofile_types.go`** — define `DeviceProfile`,
   `DeviceProfileSpec`, `DeviceProfileStatus`, `VerificationSpec`, and `Validate()`.
2. **`apis.yaml`** — add `DeviceProfile` to the `resources` list under `hardware.fabrica.dev/v1`.
3. **`fabrica add resource DeviceProfile` + `fabrica generate`** — produces generated
   handlers, routes, and storage adapter functions.
4. **`pkg/deviceProfiles/registry.go`** — thread-safe registry over `v1.DeviceProfile`.
5. **`pkg/deviceProfiles/loader.go`** — YAML parsing, default population, validation.
6. **`pkg/deviceProfiles/matcher.go`** — HTTP probe, `BuildUpdatePayload`.
7. **Unit tests** for registry, loader, and matcher.
8. **Startup wiring** in `cmd/server/main.go` — `--device-profiles-dir` flag,
   `LoadDirectory`, DB upsert via generated storage functions, registry recovery from DB.
9. **`cmd/server/deviceprofile_sync.go`** — hook generated handlers to keep
   `deviceProfiles.Global` registry in sync on create/update/delete API calls.
10. **`openapi_extensions.go`** — register `POST /deviceprofiles/reload` handler and
    add its OpenAPI path entry in `registerCustomOpenAPIPaths`.
11. **`FirmwareUpdateJobStatus.DeviceProfileID`** field + `firmwareupdatejob_types.go` update.
12. **Reconciler integration** — profile selection + payload template dispatch.
13. **Example profile files** in `device-profiles/`.
14. **`planning/HANDOFF-DEVICE_PROFILE.md`** — create this file upon completion of all
    implementation steps above.  It must document:
    - A summary of the implemented profile system (registry, loader, matcher, API).
    - How to add a new device profile (both via YAML file and via the API).
    - The exact, verified `curl` commands for each `/deviceprofiles` endpoint.
    - How the reconciler selects a profile and builds the update payload.
    - Any known limitations or edge cases discovered during implementation.

---

## 13. Out of Scope for This Plan

- Hot-reload via filesystem watch (the `/reload` API endpoint is sufficient).
- Profile versioning or rollback.
- Profile import from a remote URL.
- Authentication / authorization on the `/deviceprofiles` endpoints (assumed handled
  by existing middleware).
- A `fabrica add resource DeviceProfile` reconciler — profiles are configuration
  objects and do not drive a reconciliation loop.
