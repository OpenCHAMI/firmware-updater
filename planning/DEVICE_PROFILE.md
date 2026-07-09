# Device Profile Plan

## Goal
Define a single device profile structure that captures Redfish discovery and verification details per platform, then maintain an array of profiles assembled from separate per-device files.

The device profile is used to determine what kind of device is being updated, the Redfish paths needed to read important fields, and the update payload required for that device.
Because these values vary by device, they should be stored in the profile structure instead of being hardcoded in update logic.
Device profiles are compiled into the codebase; adding support for an additional device requires a code revision.

## Planned Data Structure
Create a `DeviceProfile` structure with these fields:

- `id`: unique identifier for the device family.
- `manufacturerPath`: Redfish path used to read the manufacturer.
- `modelPath`: Redfish path used to read the model name.
- `supportsInventoryExpand`: boolean indicating whether inventory expand is supported.
- `enabled`: boolean to control whether this profile is active; defaults to `true`.
- `defaultUpdateCommand`: default Redfish update command/action for this device.
- `defaultUpdatePayload`: default update payload template for this device, typed as `json.RawMessage`.
- `verification`: nested structure with verification rules.

Create a nested `verification` structure with these fields:

- `path`: Redfish path to read for verification.
- `filter`: field selector/key to inspect at that path.
- `value`: expected value for comparison; can be a regular expression.

## Planned File Organization
Store profile definitions in a dedicated package directory under `pkg` called `deviceProfiles` so each profile is isolated and easy to extend later.

Suggested layout:

- `pkg/deviceProfiles/types.go`: shared `DeviceProfile` and `Verification` structures.
- `pkg/deviceProfiles/registry.go`: central `DeviceProfiles` array that references per-device variables.
- `pkg/deviceProfiles/crayex.go`: the `CrayEX` profile entry.
- `pkg/deviceProfiles/ilo.go`: the `iLO` profile entry.

When adding new devices in the future, add one new file per device and register it in `registry.go`.

For now, initialize two profile entries:

1. `id: CrayEX`
2. `id: iLO`

For both entries, leave remaining string fields as `""`.
Set booleans to `false` unless intentionally overridden, with `enabled` defaulting to `true` via constructor.

## Profile Resolution And Usage Flow
Implement profile handling in this order:

1. During update preparation, run each enabled profile verification against the target device Redfish endpoint.
2. Select the first matching profile and store its `id` on the device record.
3. For all manufacturer and model lookups, use the profile referenced by the stored device profile `id`.
4. For all update requests, use `defaultUpdateCommand` and `defaultUpdatePayload` from the selected profile.
5. Replace all `%...%` placeholders in command and payload with runtime values before dispatch.

If no profile matches verification, fail with a clear terminal status and reason.

## Suggested Example Shape (Pseudo-Go)
```go
import "encoding/json"

// types.go
type Verification struct {
    Path     string
    Filter   string
    Value    string // literal or regex
}

type DeviceProfile struct {
    ID                     string
    ManufacturerPath       string
    ModelPath              string
    SupportsInventoryExpand bool
    Enabled                bool
    DefaultUpdateCommand   string
    DefaultUpdatePayload   json.RawMessage
    Verification           Verification
}

func NewDeviceProfile(id string) DeviceProfile {
    return DeviceProfile{
        ID:                     id,
        Enabled:                true,
        SupportsInventoryExpand: false,
        DefaultUpdateCommand:   "/redfish/v1/UpdateService/Actions/SimpleUpdate",
    }
}

// crayex.go
var CrayEXProfile = func() DeviceProfile {
    p := NewDeviceProfile("CrayEX")
    p.ManufacturerPath = "/redfish/v1/Chassis/Enclosure"
    p.ModelPath = "/redfish/v1/Chassis/Enclosure"
        p.DefaultUpdatePayload = json.RawMessage(`{
    "ImageURI": "%httpFileName%",
    "TransferProtocol": "HTTP",
    "Targets": [
        "/redfish/v1/UpdateService/FirmwareInventory/%target%"
    ]
}`)
    p.Verification = Verification{
        Path:   "/redfish/v1/UpdateService/FirmwareInventory/BMC",
        Filter: ".SoftwareId",
        Value:  "^(nc|cc|sc)*",
    }
    return p
}()

// ilo.go
var ILOProfile = func() DeviceProfile {
        p := NewDeviceProfile("iLO")
        p.ManufacturerPath = "/redfish/v1/Chassis/1"
        p.ModelPath = "/redfish/v1/Chassis/1"
        p.SupportsInventoryExpand = true
        p.DefaultUpdatePayload = json.RawMessage(`{
    "ImageURI": "%httpFileName%"
}`)
    p.Verification = Verification{
        Path:   "/redfish/v1/UpdateService/FirmwareInventory/1",
        Filter: ".Name",
        Value:  "iLO*",
    }
        return p
}()

// registry.go
var DeviceProfiles = []DeviceProfile{
    CrayEXProfile,
    ILOProfile,
}
```

This approach keeps `enabled` defaulted to `true` without needing to set it in each device profile file.
It also centralizes default update behavior in one place.

## Next Implementation Step
When moving from plan to code, place this in a shared package used by Redfish discovery logic and add unit tests for:

- ID matching/lookup.
- Regex verification behavior and first-match profile selection.
- Device record persists selected profile `id`.
- Manufacturer/model lookups resolve using profile paths.
- Update command and payload template expansion for `%...%` placeholders.
- Default/empty field handling.
- Registry includes all per-device files.

## Completion Handoff Requirement
Upon completion of the implementation, create `planning/HANDOFF_DEVICE_PROFILE.md` to document:

- What was implemented.
- Final device profile definitions added.
- Verification behavior and matching results.
- Update command/payload template substitution behavior.
- Any known limitations or follow-up tasks.

## Documentation Requirement
During implementation, add comments for each created structure and each created function.
