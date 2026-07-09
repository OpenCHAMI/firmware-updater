# Firmware Updater

## TL;DR: Copy/Paste Runbook

Use this if you just want the shortest path to run a full-cabinet universal campaign and to see the user workflow.

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
- [ORAS rules](#1-how-to-push-images-correctly-with-oras)
- [How OCI paths are derived](#2-how-to-identify-required-oci-paths)
- [How target Redfish paths are derived](#3-how-to-identify-required-redfish-target-paths)
- [Credentials model](#4-how-credentials-work)
- [Troubleshooting](#8-troubleshooting-quick-reference)

## Current Validated State (2026-07-09)

Validated scenarios:
- Single BMC updating multiple device targets.
- Multi-target campaign with mixed hardware, where one BMC had multiple firmware components and those component jobs executed sequentially.

What was validated:
- Campaign creation and child job fan-out.
- Universal discovery creating multiple jobs from Redfish inventory.
- ORAS annotations being used to discover compatible artifacts.
- Sequential execution per target address (no concurrent active child jobs for the same target in one campaign).
- Failure handling and error propagation into `status.errorDetail`.

Observed result from latest run:
- Campaign produced 3 child jobs total.
- All 3 failed intentionally due to dummy payloads or Redfish rejection.
- This behavior was expected for this test.

## What `FirmwareUpdateCampaign` Supports

`FirmwareUpdateCampaign` supports three modes:

1. Explicit mode:
- Set `spec.ociReference` and `spec.component`.

2. Component discovery mode:
- Set `spec.discovery.repository`, `spec.discovery.hardwareModel`, `spec.discovery.version`, and `spec.component`.

3. Universal cabinet discovery mode:
- Set only `spec.discovery.repository` (omit `spec.component` and `spec.ociReference`).
- Controller discovers firmware inventory members for each target and evaluates each component independently.

Validation rules enforced by API type validation:
- `spec.serverProxyAddress` is required.
- `spec.targets[]` is required; each entry must include `targetAddress` and `secretID`.
- `spec.ociReference` and `spec.discovery` are mutually exclusive.
- At least one of `spec.ociReference` or `spec.discovery` must be present.
- If campaign uses `spec.ociReference`, `spec.component` must be set.
- If campaign uses `spec.discovery` with `spec.component`, then `spec.discovery.hardwareModel` and `spec.discovery.version` are required.

## 1. How To Push Images Correctly With ORAS

The resolver only considers manifests that satisfy all of these:
- `artifactType` is exactly `application/vnd.openchami.firmware.bundle.v1+json`.
- The manifest has at least one layer.
- `org.opencontainers.image.version` is present and parseable as semver (supports optional leading `v`).
- Compatibility annotation matches (rules below).

Compatibility annotation key:
- `dev.fabrica.hardware.compatible`

Version annotation key:
- `org.opencontainers.image.version`

Recommended payload layer media type used in examples:
- `application/vnd.openchami.firmware.payload.v1`

### Validated ORAS pushes from latest run

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

### Compatibility matching behavior

- Matching is token-based and case-insensitive.
- The compatibility annotation can be comma/semicolon/newline separated.
- Universal discovery compares annotation tokens against many hardware hints extracted from Redfish member details (`Id`, `Name`, `Description`, `Model`, `SKU`, `PartNumber`, `SoftwareId`, `@odata.id`, related items, and target address).

## 2. How To Identify Required OCI Paths

### Component discovery mode

You explicitly provide `spec.discovery.repository` (for example `registry/firmware/bmc`) and `spec.component`.

### Universal cabinet discovery mode

Given base repository `X` from `spec.discovery.repository`, for each discovered inventory component identifier the controller tries repositories in this order:

1. `X/<slug>`
2. `X/<compact-slug>` (same slug with hyphens removed, if different)
3. `X` (base fallback)

Slug generation:
- Source identifier is component `Id`, else `Name`, else `@odata.id`.
- Lowercase.
- Replace non `[a-zA-Z0-9-]` chars with `-`.
- Trim leading/trailing `-`.

Examples:
- `BMC` -> `bmc`
- `FPGA0` -> `fpga0`
- `Cabinet Controller` -> `cabinet-controller`, then `cabinetcontroller`, then base repo

If a candidate repository returns 404, that candidate is skipped and the reconciler continues searching remaining candidates for that component.

## 3. How To Identify Required Redfish Target Paths

A firmware update eventually needs `spec.targets` in each child `FirmwareUpdateJob` (Redfish inventory member URIs).

How these are set:

1. Component mode (job or non-universal campaign child):
- If `spec.component` is set and `spec.targets` is empty, controller queries:
  - `GET https://<target>/redfish/v1/UpdateService/FirmwareInventory`
- For each member, it fetches member detail and matches component string (case-insensitive substring) against `Id`, `Name`, or `Description`.
- Matching `@odata.id` values become `spec.targets`.

2. Universal campaign mode:
- Controller discovers inventory members directly and creates child jobs with exactly one target URI per discovered component (`spec.targets = [componentURI]`).

3. Manual override:
- You may set `spec.targets` directly in a `FirmwareUpdateJob` if auto-discovery is not suitable.

### Practical way to discover paths yourself

```bash
curl -sk -u <user>:<pass> https://<bmc>/redfish/v1/UpdateService/FirmwareInventory | jq
```

Then inspect each member URI and details:

```bash
curl -sk -u <user>:<pass> https://<bmc>/redfish/v1/UpdateService/FirmwareInventory/<member> | jq
```

## 4. How Credentials Work

Credentials are resolved from encrypted secret store entries by `spec.secretID`.

Requirements:
- `MASTER_KEY` must be set and must be a 64-char hex string (32 bytes decoded; AES-256).
- Server must be started with a secrets file path that exists.
- The same `MASTER_KEY` must be used to write and read secrets.

Write credentials:

```bash
export MASTER_KEY="$(openssl rand -hex 32)"
go run ./cmd/secret-cli \
  --secret-id x9000-bmc \
  --username root \
  --password initial0 \
  --store-path ./secrets.json
```

Start server:

```bash
go run ./cmd/server serve \
  --port 8090 \
  --database-url="file:hpc_test.db?cache=shared&_fk=1" \
  --secrets-file ./secrets.json
```

Notes:
- Registry auth is optional and configured separately via server config/env (`quay_username`, `quay_password`).
- Secret value content is JSON containing non-empty `username` and `password`.

## 5. How To Update A Full Cabinet (Universal Campaign)

Use a single campaign with:
- `spec.discovery.repository` set to a base repository.
- Multiple entries in `spec.targets`.
- No `spec.component` and no `spec.ociReference`.

### Validated campaign submission from latest run

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

### Verify campaign and child jobs

```bash
curl -sS http://127.0.0.1:8090/firmwareupdatecampaigns/ | jq
curl -sS http://127.0.0.1:8090/firmwareupdatejobs/ | jq
```

Expected behavior from validated run:
- One campaign expanded into three child jobs.
- Two jobs were for the same target (`x9000c3s7b1`) and ran sequentially because campaign reconciliation allows only one active child per target at a time.

## 6. State Model And What To Watch

Campaign states:
- `Pending`
- `InProgress`
- `Completed`
- `Failed`
- `CompletedWithErrors`

Campaign summary:
- `status.summary.total`
- `status.summary.completed`
- `status.summary.failed`
- `status.summary.pending`

Child visibility:
- `status.childJobs[]` includes `targetAddress`, `jobUID`, `jobState`, and `errorDetail`.

Job states:
- `Pending` -> `Resolving` -> `InProgress` -> terminal (`Completed` or `Failed`)

## 7. Essential Operational Notes

1. Proxy address/port behavior
- The update payload URI sent to Redfish is built as `http://<serverProxyAddress>:8090/firmware-proxy/layer/<digest>`.
- This currently uses port `8090` in reconciler logic.
- Ensure BMCs can route to that IP:port.

2. TLS behavior
- Redfish calls use HTTPS with TLS verification disabled (`InsecureSkipVerify`).

3. Retry behavior
- Key network/discovery operations use exponential backoff retries (up to 4 attempts).

4. Version comparison
- OCI annotation versions are strict semver.
- Installed Redfish version may include extended text; reconciler extracts semver substring when possible.
- If installed version cannot be normalized, resolver treats newest compatible candidate as update-available.

5. Repository structure strategy
- In universal mode, create component-specific repos under a base path to control update eligibility by component.
- Missing repos (404) are effectively treated as “no update candidate here”.

## 8. Troubleshooting Quick Reference

- Error: `spec.discovery.hardwareModel must be provided when spec.component is set`
  - Cause: campaign used component discovery mode without full discovery fields.
  - Fix: include `hardwareModel` and `version`, or switch to universal mode.

- Error: `spec.secretID is required` or secret decode/load failures
  - Cause: missing/invalid secret mapping or invalid secret JSON payload.
  - Fix: rewrite secret with `secret-cli` and ensure same `MASTER_KEY`.

- Error: `http status 400: Redfish returned 400 Bad Request`
  - Cause: target rejected request/payload.
  - Fix: validate payload format expected by that component and Redfish endpoint behavior.

- Error: `Required 'version' file was missing from firmware archive.`
  - Cause: payload accepted far enough for inventory/task response to report archive-content issue.
  - Fix: package firmware payload with required internal files for that target.

- No child jobs for a component in universal mode
  - Cause: no compatible manifest, no semver-valid version annotation, or repository path mismatch.
  - Fix: verify repo naming/slugs and annotations.

## 9. Minimal End-To-End Checklist

1. Export valid `MASTER_KEY`.
2. Write target credentials via `secret-cli`.
3. Start server with `--secrets-file` and reachable `--port`.
4. Push firmware bundles with required ORAS artifact type and annotations.
5. Submit campaign (component or universal mode).
6. Watch campaign and job status endpoints until terminal states.
7. Inspect `errorDetail` fields for actionable failures.

---

This TEMP README is intentionally focused on current behavior and tested workflow, and is meant to replace scattered/older guidance while this PR is in review.
