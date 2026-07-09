# Firmware Updater TEMP README

Current source of truth for FirmwareUpdateCampaign usage in this repository.

Last validated: 2026-07-09.

## TL;DR: Copy/Paste Runbook

Use this if you just want the shortest path to run a full-cabinet universal campaign.

### 1) Set key and write BMC credentials

```bash
export MASTER_KEY="$(openssl rand -hex 32)"

go run ./cmd/secret-cli \
  --secret-id x9000-bmc \
  --username root \
  --password initial0 \
  --store-path ./secrets.json
```

### 2) Start server

```bash
go run ./cmd/server serve \
  --port 8090 \
  --database-url="file:hpc_test.db?cache=shared&_fk=1" \
  --secrets-file ./secrets.json
```

### 3) Push firmware artifacts with required ORAS metadata

```bash
oras push 127.0.0.1:5000/firmware/bmc:99.99.99 \
  --plain-http \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  --annotation "dev.fabrica.hardware.compatible=x9000" \
  --annotation "org.opencontainers.image.version=99.99.99" \
  dummy_firmware:application/vnd.openchami.firmware.payload.v1

oras push 127.0.0.1:5000/firmware/fpga0:99.99.99 \
  --plain-http \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  --annotation "dev.fabrica.hardware.compatible=FPGA0" \
  --annotation "org.opencontainers.image.version=99.99.99" \
  dummy_firmware:application/vnd.openchami.firmware.payload.v1

oras push 127.0.0.1:5000/firmware/17:3.0.0 \
  --plain-http \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  --annotation "org.opencontainers.image.version=3.0.0" \
  --annotation "dev.fabrica.hardware.compatible=Embedded Video Controller,102b0538159000e4" \
  ./dummy-video.bin:application/octet-stream
```

### 4) Submit universal campaign

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatecampaigns \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "live-cray-auto-cabinet"
    },
    "spec": {
      "serverProxyAddress": "10.254.1.20",
      "targets": [
        {
          "targetAddress": "x9000c3s7b1",
          "secretID": "x9000-bmc"
        },
        {
          "targetAddress": "x3000c0s21b0",
          "secretID": "x9000-bmc"
        }
      ],
      "discovery": {
        "repository": "127.0.0.1:5000/firmware"
      }
    }
  }'
```

### 5) Watch campaign and jobs

```bash
curl -sS http://127.0.0.1:8090/firmwareupdatecampaigns/ | jq
curl -sS http://127.0.0.1:8090/firmwareupdatejobs/ | jq
```

If you need details, jump to:
- [ORAS rules](#oras-rules-required-for-discovery)
- [How OCI paths are derived](#how-oci-paths-are-derived)
- [How target Redfish paths are derived](#how-target-redfish-paths-are-derived)
- [Credentials model](#credentials-model)
- [Troubleshooting](#troubleshooting)

## Quick Facts

- Validated with:
  - Single BMC with multiple component targets.
  - Multiple target systems in one campaign.
- Observed in latest run:
  - 1 campaign expanded to 3 child jobs.
  - Jobs for the same target were sequenced (not concurrent).
  - All failed intentionally due to dummy payloads and expected Redfish rejection paths.

## Campaign Modes

1. Explicit mode
   - Set spec.ociReference and spec.component.
2. Component discovery mode
   - Set spec.discovery.repository + spec.discovery.hardwareModel + spec.discovery.version + spec.component.
3. Universal cabinet discovery mode
   - Set only spec.discovery.repository.
   - Omit spec.component and spec.ociReference.

## ORAS Rules Required For Discovery

The resolver only considers manifests that meet all of the following.

| Field | Required value |
| --- | --- |
| Artifact type | application/vnd.openchami.firmware.bundle.v1+json |
| Compatibility annotation | dev.fabrica.hardware.compatible |
| Version annotation | org.opencontainers.image.version |
| Version format | Semantic version (v prefix allowed) |
| Layers | At least one layer |

Compatibility matching is token-based and case-insensitive.

## How OCI Paths Are Derived

### Component discovery mode

You provide spec.discovery.repository directly (example: 127.0.0.1:5000/firmware/bmc).

### Universal mode

From base repository X and discovered component identifier, the reconciler tries:

1. X/slug
2. X/compact-slug (hyphens removed)
3. X (base fallback)

Slug behavior:
- Lowercase identifier.
- Replace non alphanumeric/hyphen chars with hyphen.
- Trim leading/trailing hyphens.

Examples:
- BMC -> bmc
- FPGA0 -> fpga0
- Cabinet Controller -> cabinet-controller -> cabinetcontroller -> base repo

404 on one candidate path is treated as skip-and-continue.

## How Target Redfish Paths Are Derived

Child jobs need spec.targets (Redfish firmware inventory member URIs).

1. If spec.component is set and spec.targets is empty:
   - Reconciler queries:
     - GET https://<target>/redfish/v1/UpdateService/FirmwareInventory
   - It reads each member and matches component text against Id, Name, or Description.
   - Matching member @odata.id values become spec.targets.

2. In universal campaign mode:
   - Each discovered component becomes one child job with one explicit target URI.

3. Manual override:
   - You can set spec.targets directly in a FirmwareUpdateJob.

Quick path discovery commands:

```bash
curl -sk -u <user>:<pass> https://<bmc>/redfish/v1/UpdateService/FirmwareInventory | jq
curl -sk -u <user>:<pass> https://<bmc>/redfish/v1/UpdateService/FirmwareInventory/<member> | jq
```

## Credentials Model

- Credentials are stored by secret ID and loaded from encrypted secrets file.
- MASTER_KEY must be a 64-character hex string (32-byte AES-256 key).
- The same MASTER_KEY must be used by secret-cli and server.
- Secret JSON value must contain non-empty username and password.

Registry auth is separate (quay username/password config).

## Behavior You Should Expect

### Campaign states

- Pending
- InProgress
- Completed
- Failed
- CompletedWithErrors

### Job states

Pending -> Resolving -> InProgress -> Completed or Failed

### Operational constraints

- Redfish payload URL is built as:
  - http://<serverProxyAddress>:8090/firmware-proxy/layer/<digest>
- Port 8090 is currently hardcoded in reconciler payload URL generation.
- BMCs must be able to reach serverProxyAddress:8090.
- Redfish HTTPS uses insecure TLS verification.
- Network/discovery operations use retry with backoff.

## Troubleshooting

### spec.discovery.hardwareModel must be provided when spec.component is set

Cause:
- Component discovery mode requires hardwareModel and version.

Fix:
- Provide discovery.hardwareModel and discovery.version, or use universal mode.

### spec.secretID is required / secret decode or load failures

Cause:
- Missing secret mapping, wrong MASTER_KEY, or malformed secret payload.

Fix:
- Rewrite secret with secret-cli and verify the same MASTER_KEY is used.

### http status 400: Redfish returned 400 Bad Request

Cause:
- Target rejected request or payload.

Fix:
- Verify payload format and component endpoint expectations.

### Required 'version' file was missing from firmware archive.

Cause:
- Payload archive format/content did not match platform expectation.

Fix:
- Rebuild firmware archive to include required internal metadata/files.

### No child jobs created for a component in universal mode

Cause:
- No compatible manifest, semver annotation invalid/missing, or repository path mismatch.

Fix:
- Verify repository naming and required annotations.

---

This TEMP README intentionally prioritizes practical execution and current behavior over historical context.
