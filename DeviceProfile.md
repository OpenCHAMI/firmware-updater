# Device Profile

## Overview

A device profile describes how the service recognizes a hardware target and how it sends a firmware update request to that target over Redfish. Profiles are the runtime customization layer for vendor-specific behavior. They let the same firmware execution service support multiple BMC families without changing the `FirmwareUpdateJob` payload format.

Profiles are stored as Fabrica resources and can be loaded from disk at startup or managed through the HTTP API. The server keeps all loaded profiles in a thread-safe in-memory registry and uses them during reconciliation to match a target device, read device identity, and build the update payload.

## What a Device Profile Controls

A profile defines:

- How the service identifies a device family.
- Which Redfish update action URI to call.
- Which HTTP method to use for the update request.
- How to build the request body using placeholder substitution.
- Whether the platform supports OData inventory expansion.
- Which Redfish probe to use when selecting a profile for a target.

In practice, that means the same service can handle different Redfish layouts, different update endpoints, and different payload formats for iLO, Cray EX, or other vendors.

## Runtime Flow

At a high level, the service uses a profile like this:

1. Load profiles from the configured `device-profiles` directory, or create/update them through the API.
2. Probe the target device with profile verification rules until one profile matches.
3. Read manufacturer and model information using the profile's identity paths.
4. Render the update payload template using runtime substitutions such as `imageURI` and `target`.
5. Send the Redfish update request to the profile's update action URI using the profile's HTTP method.

If no enabled profile matches the target, the update job fails before dispatch.

## API Resource Shape

DeviceProfile is a Fabrica resource with the standard Kubernetes-style envelope:

- `apiVersion`
- `kind`
- `metadata`
- `spec`
- `status`

### Go Structure

```go
type DeviceProfile struct {
    APIVersion string              `json:"apiVersion"`
    Kind       string              `json:"kind"`
    Metadata   fabrica.Metadata    `json:"metadata"`
    Spec       DeviceProfileSpec   `json:"spec" validate:"required"`
    Status     DeviceProfileStatus `json:"status,omitempty"`
}
```

### Spec Structure

```go
type DeviceProfileSpec struct {
    ProfileID string `json:"profileID" validate:"required" yaml:"id"`
    Name string `json:"name" yaml:"name"`
    Enabled bool `json:"enabled" yaml:"enabled"`

    UpdateActionURI string `json:"updateActionURI" validate:"required" yaml:"updateActionURI"`
    UpdatePayloadTemplate string `json:"updatePayloadTemplate" validate:"required" yaml:"updatePayloadTemplate"`
    UpdateMethod string `json:"updateMethod" yaml:"updateMethod"`

    ManufacturerPath string `json:"manufacturerPath" validate:"required" yaml:"manufacturerPath"`
    ManufacturerField string `json:"manufacturerField" validate:"required" yaml:"manufacturerField"`
    ModelPath string `json:"modelPath" validate:"required" yaml:"modelPath"`
    ModelField string `json:"modelField" validate:"required" yaml:"modelField"`

    SupportsInventoryExpand bool `json:"supportsInventoryExpand" yaml:"supportsInventoryExpand"`
    FirmwareInventoryExpandParam string `json:"firmwareInventoryExpandParam" yaml:"firmwareInventoryExpandParam"`

    Verification VerificationSpec `json:"verification" yaml:"verification"`
}
```

### Verification Structure

```go
type VerificationSpec struct {
    Path string `json:"path" yaml:"path"`
    Field string `json:"field" yaml:"field"`
    Pattern string `json:"pattern" yaml:"pattern"`
}
```

### Status Structure

```go
type DeviceProfileStatus struct {
    SourceFile string `json:"sourceFile,omitempty"`
    LoadedAt string `json:"loadedAt,omitempty"`
}
```

## Field Details

### `spec.profileID`

The stable identifier for the profile. It is used as the registry key and must be unique. The ID must match the pattern `[a-z0-9_-]+`.

### `spec.name`

Human-readable name for display and operational use.

### `spec.enabled`

Controls whether the profile participates in device matching. Disabled profiles remain stored but are skipped during probe-based selection.

### `spec.updateActionURI`

The Redfish action URI used for the update request. It must start with `/`. Relative paths are resolved by the dispatch logic against the target device base URL.

### `spec.updatePayloadTemplate`

A JSON template rendered at dispatch time. The service substitutes placeholders such as:

- `%imageURI%`
- `%target%`
- `%component%`
- `%applyTime%`

The rendered payload must still be valid JSON.

### `spec.updateMethod`

The HTTP verb used for the update request. The loader and API normalization default it to `POST` when empty.

### `spec.manufacturerPath` and `spec.manufacturerField`

Used to read the manufacturer string from the target device through Redfish.

### `spec.modelPath` and `spec.modelField`

Used to read the model string from the target device through Redfish.

### `spec.supportsInventoryExpand`

Indicates whether the platform supports a single-call inventory listing using an OData expand query on the discovered `FirmwareInventory` collection URI.

### `spec.firmwareInventoryExpandParam`

The OData query string appended when inventory expansion is supported. If the field is empty and expansion is enabled, the default is `?$expand=.`.

### `spec.verification`

Describes the probe used to determine whether a profile applies to a target device. The service reads the specified Redfish path, extracts the specified field, and optionally matches the value against a regular expression.

## Validation Rules

The resource validation logic enforces the following rules:

- `spec.profileID` is required.
- `spec.profileID` must match `[a-z0-9_-]+`.
- `spec.updateActionURI` must start with `/`.
- `spec.verification.pattern`, when present, must compile as a valid regular expression.

The loader also applies defaults before validation:

- `spec.updateMethod` defaults to `POST`.
- `spec.firmwareInventoryExpandParam` defaults to `?$expand=.` when inventory expansion is enabled and no value is provided.

## YAML File Format

Profiles are commonly stored on disk as YAML files in the default `./device-profiles/` directory.

Example:

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

## Registry and Loading

Loaded profiles are kept in a process-wide registry backed by a map keyed by `spec.profileID`.

Important registry operations:

- `Register` adds a new profile and rejects duplicate IDs.
- `Get` returns a profile by ID.
- `List` returns all profiles currently in memory.
- `Delete` removes a profile by ID.
- `Upsert` inserts or replaces a profile with the same ID.

At startup, the server scans the configured device-profile directory, loads all `*.yaml` and `*.yml` files, and stores the resulting profiles in the registry. Each loaded profile records its source file path and load time in status.

## HTTP API

The server exposes the following endpoints under `/deviceprofiles`.

### List profiles

`GET /deviceprofiles`

Returns the current registry contents.

Response shape:

```json
{
  "items": [
    {
      "apiVersion": "hardware.fabrica.dev/v1",
      "kind": "DeviceProfile",
      "metadata": {
        "name": "ilo"
      },
      "spec": {
        "profileID": "ilo"
      },
      "status": {
        "sourceFile": "./device-profiles/ilo.yaml",
        "loadedAt": "2026-07-16T12:34:56Z"
      }
    }
  ],
  "count": 1
}
```

### Get a profile

`GET /deviceprofiles/{id}`

Returns a single profile by its `profileID`.

### Create a profile

`POST /deviceprofiles`

Creates a new profile from a `DeviceProfile` JSON body.

Behavior:

- The body is decoded as a full profile object.
- Missing envelope defaults are populated by the server.
- The profile is validated before being registered.
- Duplicate IDs return `409 Conflict`.

### Replace a profile

`PUT /deviceprofiles/{id}`

Replaces the profile identified by the URL path.

Behavior:

- The path ID is authoritative and overwrites `spec.profileID`.
- The profile is fully normalized and validated.
- The registry entry is replaced or created.

### Patch a profile

`PATCH /deviceprofiles/{id}`

Applies a partial merge over the existing profile.

Behavior:

- The existing profile is loaded first.
- The request body is decoded on top of the current profile.
- The path ID remains authoritative.
- The merged profile is validated and stored.

### Delete a profile

`DELETE /deviceprofiles/{id}`

Deletes the profile from the registry.

### Reload profiles

`POST /deviceprofiles/reload`

Rescans the configured device-profile directory and refreshes the in-memory registry.

Response shape:

```json
{
  "loaded": 2,
  "errors": []
}
```

## Example Profiles

The repository includes example YAML profiles in `device-profiles/` such as:

- `ilo.yaml` for HPE iLO 5/6
- `crayex.yaml` for HPE Cray EX

These examples are useful starting points for new vendors or platform variants.

## When To Use Device Profiles

Use a device profile when the target hardware needs any of the following:

- A different Redfish update action URI.
- A different request body shape.
- Different identity paths for manufacturer and model.
- A different verification probe for profile selection.
- A different inventory expansion strategy.

If a platform behaves like a standard Redfish device and does not need vendor-specific behavior, a minimal profile can still be used to keep the update path explicit and consistent.

## Summary

Device profiles are the main extension point for vendor-specific Redfish behavior. They are loaded into memory, exposed through a CRUD API, used during reconciliation to select the right target behavior, and persisted in the resource envelope used throughout Fabrica.
