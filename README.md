# JIT Firmware Execution Service

## Overview

The Firmware Execution Service is a stateless orchestration engine designed to deploy firmware binaries directly from OCI registries to hardware controllers (BMCs, Chassis Controllers, Cabinet Controllers) using the Redfish standard.

The service maintains zero local inventory. It utilizes a declarative, on-demand execution model driven by the `FirmwareUpdateJob` resource. Upon receiving a job, the service dynamically queries the target hardware's Redfish `UpdateService` to discover its specific update URI. It then establishes a direct stream from the upstream OCI registry, utilizing `io.Copy` to flush the bytes directly into the hardware's response buffer without writing the payload to local disk.

## Operating Modes

The service supports two distinct operating methods for payload resolution:

1. **Discovery Mode:** The service autonomously queries an OCI repository using the ORAS protocol. It downloads available manifests, filters them by matching a requested hardware model against OCI annotations, parses the version annotations, and automatically resolves the highest semantic version (e.g., when targeting `latest`).
2. **Explicit Mode:** A manual override pathway where the user provides the exact OCI path and SHA digest, bypassing all annotation filtering and version resolution.

## Prerequisites

* **Go Toolchain:** Required to compile and run the Fabrica service.
* **ORAS CLI:** Required by publishers to attach strict OCI annotations when uploading firmware binaries to the registry.
* **Network Routing:** The `serverProxyAddress` property specified in the job payload must be an IPv4 address accurately routable from the isolated management VLAN hosting the physical hardware. If misconfigured, the target hardware will time out when attempting to pull the payload stream.

## Device Profiles

Device profiles define how the service identifies a target device and how it builds the Redfish update request for that device family. They are the runtime layer that lets the same service support different BMC vendors and platforms without changing the job payload format.

For full API details, structures, and operational behavior, see [DeviceProfile.md](DeviceProfile.md).

The server loads profiles from the default `./device-profiles/` directory at startup. Example profiles are included for HPE iLO and HPE Cray EX. If you add or change a profile file, restart the server or call the `/deviceprofiles/reload` endpoint to rescan the directory.

Each profile can define:

* The Redfish paths and fields used to match a device.
* The update action URI and HTTP method used to send the firmware request.
* The payload template used to substitute values such as `%imageURI%` and `%target%`.
* Whether the platform supports OData inventory expansion, and which expand query string to use.

Use a device profile when a platform needs a different Redfish action, a different payload shape, or a different way to discover identity and firmware inventory. During reconciliation, the service matches the target against the loaded profiles and records the selected profile in job status.

To add a new profile, copy one of the examples in `device-profiles/`, adjust the `id`, `verification`, and Redfish update settings, then reload the profiles. If no profile matches a target, the update job fails before dispatch.

## Publisher Workflow: Staging Firmware in the OCI Registry

For Discovery Mode to function, the service must trust the metadata in the OCI registry. Publishers must push the firmware binary using ORAS and attach specific annotations to the manifest.

* `dev.fabrica.hardware.compatible`: A string or comma-separated list defining the hardware models the binary supports.
* `org.opencontainers.image.version`: The strict Semantic Version of the payload.

### Push Command

```bash
oras push 127.0.0.1:5000/firmware/cray-bmc:1.10.2 \
  --plain-http \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  --annotation "dev.fabrica.hardware.compatible=x9000" \
  --annotation "org.opencontainers.image.version=1.10.2" \
  NC-1.10.2-22-s.tar.gz:application/vnd.openchami.firmware.payload.v1

```

### Output

```text
✓ Exists    NC-1.10.2-22-s.tar.gz                    56.1/56.1 MB 100.00%      └─ sha256:827a78b2484e60492c914b9567df487b6c5d647a13dceae13f93ecbf1cb44b14
✓ Exists    application/vnd.oci.empty.v1+json              2/2  B 100.00%      └─ sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a
✓ Uploaded  application/vnd.oci.image.manifest.v1+json 713/713  B 100.00%      └─ sha256:5a4a38b79a925da16f1f69707140a66ec462c40a3ed474b30ecd50f1f0cb4f05
Pushed [registry] 127.0.0.1:5000/firmware/cray-bmc:1.10.2
ArtifactType: application/vnd.openchami.firmware.bundle.v1+json
Digest: sha256:5a4a38b79a925da16f1f69707140a66ec462c40a3ed474b30ecd50f1f0cb4f05

```

## End-User Workflow: Executing a Firmware Update Job

To initiate an update, the user submits a `FirmwareUpdateJob` resource. In Discovery Mode, the user specifies the hardware model (`x9000`), the repository path, and the target version (`latest`). The user does not need to know the SHA digest.

Before submitting the job, write encrypted BMC credentials to the local secrets store using the out-of-band CLI:

```bash
export MASTER_KEY="$(openssl rand -hex 32)"
go run ./cmd/secret-cli \
  --secret-id x9000-bmc \
  --username root \
  --password initial0 \
  --store-path ./secrets.json

```

Start the server with the same `MASTER_KEY` and mounted/provisioned store path:

```bash
MASTER_KEY="$MASTER_KEY" ./tmp/server serve --secrets-file ./secrets.json

```

### Submit Job Command

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "live-cray-discovery-bmc"
    },
    "spec": {
      "targetAddress": "x9000c3s7b1",
      "secretID": "x9000-bmc",
      "serverProxyAddress": "10.254.1.20",
      "component": "BMC",
      "discovery": {
        "repository": "127.0.0.1:5000/firmware/cray-bmc",
        "hardwareModel": "x9000",
        "version": "latest"
      }
    }
  }'

```

### Output

```json
{
  "apiVersion": "v1",
  "kind": "FirmwareUpdateJob",
  "metadata": {
    "name": "live-cray-discovery-bmc",
    "uid": "firmwareupdatejob-8eab5b0e",
    "createdAt": "2026-06-17T22:32:39.066344171Z",
    "updatedAt": "2026-06-17T22:32:39.066344171Z"
  },
  "spec": {
    "targetAddress": "x9000c3s7b1",
    "secretID": "x9000-bmc",
    "discovery": {
      "repository": "127.0.0.1:5000/firmware/cray-bmc",
      "hardwareModel": "x9000",
      "version": "latest"
    },
    "component": "BMC",
    "serverProxyAddress": "10.254.1.20"
  },
  "status": {}
}

```

## Monitoring and Validation

The update process operates asynchronously on background threads. The service writes the resolved version and digest into the status block of the job once discovery completes, providing a permanent record of what "latest" evaluated to at execution time.

### Check Service Resolution Status

```bash
curl -k http://127.0.0.1:8090/firmwareupdatejobs/firmwareupdatejob-8eab5b0e

```

### Output

```json
{
  "apiVersion": "v1",
  "kind": "FirmwareUpdateJob",
  "metadata": {
    "name": "live-cray-discovery-bmc",
    "uid": "firmwareupdatejob-8eab5b0e",
    "createdAt": "2026-06-17T22:32:39.066344171Z",
    "updatedAt": "2026-06-17T22:32:42.01724319Z"
  },
  "spec": {
    "targetAddress": "x9000c3s7b1",
    "secretID": "x9000-bmc",
    "discovery": {
      "repository": "127.0.0.1:5000/firmware/cray-bmc",
      "hardwareModel": "x9000",
      "version": "latest"
    },
    "component": "BMC",
    "serverProxyAddress": "10.254.1.20"
  },
  "status": {
    "jobState": "InProgress",
    "resolvedVersion": "1.10.2",
    "resolvedDigest": "sha256:827a78b2484e60492c914b9567df487b6c5d647a13dceae13f93ecbf1cb44b14"
  }
}

```

### Verify Hardware State

Query the hardware directly via Redfish to verify it has successfully accepted the payload stream from the service.

```bash
curl -sk -u root:initial0 https://x9000c3s7b1/redfish/v1/UpdateService/FirmwareInventory/BMC

```

### Output

```json
{
  "@odata.etag": "W/\"1781735903\"",
  "@odata.id": "/redfish/v1/UpdateService/FirmwareInventory/BMC",
  "@odata.type": "#SoftwareInventory.v1_1_0.SoftwareInventory",
  "Description": "Baseboard Management Controller",
  "Id": "BMC",
  "Name": "BMC",
  "SoftwareId": "nc:*:*:*",
  "Status": {
    "Health": "OK",
    "State": "Enabled"
  },
  "Updateable": true,
  "Version": "nc.1.10.2-22-shasta-release.arm.2026-01-15T01:13:10+00:00.a0bcef9"
}

```

## Bulk Campaign Workflow

Use `FirmwareUpdateCampaign` when you want to fan firmware updates out to many targets in one request. The campaign owns the shared payload settings and a `targets` array that lists each BMC to update.

Campaigns support three modes:

1. Explicit payload mode: set `spec.ociReference` and `spec.component`.
2. Component discovery mode: set `spec.discovery` with `repository`, `hardwareModel`, and `version`, plus `spec.component`.
3. Universal cabinet discovery mode: set only `spec.discovery.repository` and omit both `spec.component` and `spec.ociReference`.

Create the campaign with a single `POST` to `/firmwareupdatecampaigns/`:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatecampaigns/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "x9000-cabinet-01"
    },
    "spec": {
      "serverProxyAddress": "10.254.1.20",
      "component": "BMC",
      "ociReference": "127.0.0.1:5000/firmware/cray-bmc:1.10.2",
      "targets": [
        {
          "targetAddress": "x9000c3s7b1",
          "secretID": "x9000-bmc"
        },
        {
          "targetAddress": "x9000c3s7b2",
          "secretID": "x9000-bmc"
        }
      ]
    }
  }'
```

The response returns the campaign UID immediately. Reconciliation then spawns one `FirmwareUpdateJob` per target and annotates each child job with the parent campaign UID so status aggregation can find the correct children later.

### Universal Cabinet Discovery

Universal mode lets the campaign inspect each target's Redfish firmware inventory and decide which components actually need an update. The campaign:

1. Crawls `/redfish/v1/UpdateService/FirmwareInventory` for every target.
2. Derives component-specific repository candidates from the configured base repository path, for example `127.0.0.1:5002/firmware/bmc` for a discovered `BMC` component.
3. Compares the installed Redfish firmware version to the highest compatible OCI manifest version.
4. Creates child jobs only for components where a newer payload exists.

Submit a universal campaign like this:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatecampaigns/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "x9000-cabinet-universal"
    },
    "spec": {
      "serverProxyAddress": "10.254.1.20",
      "discovery": {
        "repository": "127.0.0.1:5000/firmware"
      },
      "targets": [
        {
          "targetAddress": "x9000c3s7b1",
          "secretID": "x9000-bmc"
        }
      ]
    }
  }'
```

Each generated child job bypasses its own auto-discovery step by carrying the exact `spec.ociReference` and exact Redfish `spec.targets` URI discovered by the campaign.

Check progress with the campaign UID:

```bash
curl -sS http://127.0.0.1:8090/firmwareupdatecampaigns/campaign-1a2b3c4d
```

The `status` block reports the campaign state plus aggregate counts for `total`, `completed`, `failed`, and `pending`, along with the per-target child job list. In universal mode, `summary.total` counts child jobs across all discovered components, not just the number of BMC addresses. Campaigns now also report `CompletedWithErrors` when some child jobs succeed and others fail.