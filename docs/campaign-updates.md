# FirmwareUpdateCampaign Usage Guide

The `FirmwareUpdateCampaign` controller automates firmware updates across a fleet of hardware by mapping components discovered via Redfish to artifacts stored in an OCI registry. The system differentiates firmware binaries for identical components (such as a "BMC") across varying hardware vendors and models by using strict OCI image annotations.

## 1. Discovery and Resolution Mechanics

When a `FirmwareUpdateCampaign` is executed in universal discovery mode, the system connects to the Redfish endpoint (`/redfish/v1/UpdateService/FirmwareInventory`) for each target.

For each inventory member found, the system performs two actions:

1. **Component Identification:** It extracts the component name (e.g., `BMC`, `BIOS`) to determine the OCI repository to query. For a base repository of `registry.example.com/firmware`, a component named "BMC" resolves to the `registry.example.com/firmware/bmc` path.
2. **Hardware Hint Extraction:** It collects hardware details directly from the Redfish inventory member, prioritizing fields such as `Model`, `SKU`, `PartNumber`, `Name`, and `Description`.

The controller then queries the mapped OCI repository and evaluates all available manifests. It will only select a firmware payload if the extracted Redfish hardware hints match the `dev.fabrica.hardware.compatible` annotation on the OCI image.

## 2. Preparing the OCI Registry

To ensure the controller selects the correct binary for a specific piece of hardware, you must push the firmware payload to your OCI registry with the specific OpenCHAMI artifact type and the necessary compatibility annotations.

**Required OCI Metadata:**

* **Artifact Type:** `application/vnd.openchami.firmware.bundle.v1+json`
* **Version Annotation:** `org.opencontainers.image.version` (Must be a valid semantic version for comparison, e.g., `1.2.0`)
* **Compatibility Annotation:** `dev.fabrica.hardware.compatible` (A comma-separated list of strings that exactly match the expected Redfish Model, SKU, or PartNumber).

### How Universal Discovery Maps Hardware to Repositories

When using a `FirmwareUpdateCampaign` in Universal Discovery mode (where the `component` field is intentionally omitted), the reconciliation engine dynamically determines where to look for firmware updates based on the target hardware's Redfish inventory.

**The Mapping Process**

1. **Inventory Polling:** The system connects to the target's Redfish API (`/redfish/v1/UpdateService/FirmwareInventory`) and retrieves the list of all installed components.
2. **Identifier Extraction:** For each component, the system extracts the primary identifier. It prioritizes the `Id` field, followed by the `Name` field, and finally the URI string itself (e.g., `BMC`, `Node0.BIOS`, or `FPGA0`).
3. **Sanitization:** The engine normalizes this identifier to generate a valid OCI repository slug. It converts the string to lowercase and replaces any non-alphanumeric characters (such as periods or spaces) with hyphens. It also generates a secondary compact slug by removing the hyphens entirely.
* `BMC` becomes `bmc`
* `Node0.BIOS` becomes `node0-bios` (and `node0bios`)
* `FPGA0` becomes `fpga0`


4. **Path Construction:** The system appends these sanitized slugs to the base repository specified in the campaign's `discovery.repository` field. If the base configuration is `registry.example.com/firmware`, the engine automatically constructs and queries search paths like `registry.example.com/firmware/bmc` and `registry.example.com/firmware/node0-bios`.

**Controlling Component Updates via Registry Structure**
This dynamic path generation allows administrators to explicitly control which hardware components are eligible for automated updates without modifying the Kubernetes YAML payloads.

* **Opt-In by Path Creation:** To enable updates for a specific hardware component type, create an OCI repository that exactly matches its sanitized Redfish identifier (e.g., `/firmware/node0-bios`) and push the annotated firmware bundles to that path.
* **Opt-Out by Omission:** To exclude a component from automated updates, ensure no corresponding repository exists in the registry. When the reconciler calculates a search path like `/firmware/fpga0` and receives an HTTP 404 response from the OCI registry, it safely ignores that component and proceeds with evaluating the rest of the hardware inventory.

### Command Examples: Pushing Vendor-Specific Firmware

The following `oras push` commands demonstrate how to store both HPE and Dell BMC firmware in the exact same OCI repository (`/firmware/bmc`) while ensuring they are perfectly isolated by hardware model.

**Pushing HPE Firmware:**

```bash
oras push registry.example.com/firmware/bmc:1.2.0-hpe \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  --annotation "org.opencontainers.image.version=1.2.0" \
  --annotation "dev.fabrica.hardware.compatible=ProLiant DL380 Gen10,ProLiant DL360 Gen10" \
  ./hpe-ilo5-1.2.0.bin:application/octet-stream

```

**Pushing Dell Firmware:**

```bash
oras push registry.example.com/firmware/bmc:2.4.1-dell \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  --annotation "org.opencontainers.image.version=2.4.1" \
  --annotation "dev.fabrica.hardware.compatible=PowerEdge R740,PowerEdge R640" \
  ./dell-idrac9-2.4.1.exe:application/octet-stream

```

## 3. Defining the FirmwareUpdateCampaign

To instruct the system to scan your hardware targets and apply any newer firmware found in the OCI registry, submit a `FirmwareUpdateCampaign` JSON payload to the microservice utilizing the `discovery` specification.

The `spec.targets` array defines the target hardware IP addresses or hostnames, and maps them to secrets containing the basic auth credentials for that specific BMC.

### Command Example: Creating the Campaign

Use a `curl` POST request to create the campaign on the microservice endpoint:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatecampaigns \
   -H 'Content-Type: application/json' \
   -d '{
     "metadata": {
       "name": "fleet-wide-bmc-update"
     },
     "spec": {
       "serverProxyAddress": "10.0.5.50",
       "discovery": {
         "repository": "registry.example.com/firmware"
       },
       "targets": [
         {
           "targetAddress": "10.0.10.101",
           "secretID": "hpe-bmc-creds-secret"
         },
         {
           "targetAddress": "10.0.10.102",
           "secretID": "dell-bmc-creds-secret"
         },
         {
           "targetAddress": "10.0.10.103",
           "secretID": "dell-bmc-creds-secret"
         }
       ]
     }
   }'

```

## 4. Execution and Verification

Once submitted, the microservice processes the campaign. For every target where the installed firmware version is lower than the semantic version in the OCI registry (for the matching hardware), the campaign automatically spawns a child `FirmwareUpdateJob`.

You can monitor the aggregate progress of the campaign, which provides concrete counts of the underlying jobs, by querying the specific campaign by name:

```bash
curl -sS http://127.0.0.1:8090/firmwareupdatecampaigns/fleet-wide-bmc-update

```

The JSON response will contain a `status` block detailing the completion ratios and states of the generated jobs:

```json
{
  "metadata": {
    "name": "fleet-wide-bmc-update"
  },
  "spec": {
    "serverProxyAddress": "10.0.5.50",
    "discovery": {
      "repository": "registry.example.com/firmware"
    },
    "targets": [
      {
        "targetAddress": "10.0.10.101",
        "secretID": "hpe-bmc-creds-secret"
      },
      {
        "targetAddress": "10.0.10.102",
        "secretID": "dell-bmc-creds-secret"
      },
      {
        "targetAddress": "10.0.10.103",
        "secretID": "dell-bmc-creds-secret"
      }
    ]
  },
  "status": {
    "campaignState": "InProgress",
    "summary": {
      "total": 3,
      "completed": 1,
      "failed": 0,
      "pending": 2
    },
    "childJobs": [
      {
        "targetAddress": "10.0.10.101",
        "jobUID": "a1b2c3d4-...",
        "jobState": "Completed"
      },
      {
        "targetAddress": "10.0.10.102",
        "jobUID": "e5f6g7h8-...",
        "jobState": "InProgress"
      }
    ]
  }
}

```