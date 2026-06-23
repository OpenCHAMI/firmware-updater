# Sysadmin Guide: JIT Firmware Execution Service

## Overview

The JIT Firmware Execution Service orchestrates firmware updates directly from OCI registries to hardware controllers via the Redfish standard. It operates statelessly, meaning it does not store firmware inventory locally. Instead, it dynamically pulls the required payload from the registry and proxies the byte stream directly to the target hardware controller.

## 1. Prerequisites and ORAS Installation

To stage firmware with custom metadata, the ORAS (OCI Registry As Storage) command-line tool is required. The target environment assumes a Linux operating system and a Quay OCI registry.

Execute the following to install ORAS:

```bash
VERSION="1.1.0"
curl -LO "https://github.com/oras-project/oras/releases/download/v${VERSION}/oras_${VERSION}_linux_amd64.tar.gz"
mkdir -p oras-install
tar -zxf oras_${VERSION}_linux_amd64.tar.gz -C oras-install/
sudo mv oras-install/oras /usr/local/bin/
rm -rf oras_${VERSION}_linux_amd64.tar.gz oras-install/

```

Authenticate to your Quay registry before pushing firmware artifacts:

```bash
oras login quay.io

```

*You will be prompted for your Quay username and password/token.*

## 2. Deploying the Service

The service is distributed as a Docker container. Deploy it by pulling the latest image from the GitHub Container Registry.

```bash
docker run -d \
  -p 8090:8090 \
  --name firmware-updater \
  ghcr.io/openchami/firmware-updater:latest

```

**Network Routing Requirement:** The service exposes an HTTP proxy on port `8090` by default. When an update job runs, the service instructs the physical hardware controller to download the firmware directly from this proxy. Therefore, the host running this Docker container must have an IP address (referred to as the `serverProxyAddress`) that is directly routable from the hardware management VLAN. If the hardware cannot reach this IP over port 8090, the update will time out.

## 3. Staging Firmware in the OCI Registry

The service supports two operating methods: **Discovery Mode** and **Explicit Mode**.

### Discovery Mode

In Discovery Mode, the service autonomously searches a given OCI repository and resolves the highest matching semantic version for a specified hardware model. For this to function, the firmware binary must be pushed using ORAS with specific OCI annotations and artifact types.

Required parameters when pushing for Discovery Mode:

* **Artifact Type:** `application/vnd.openchami.firmware.bundle.v1+json`
* **Payload Type:** `application/vnd.openchami.firmware.payload.v1`
* **Annotation 1:** `dev.fabrica.hardware.compatible` (The hardware model)
* **Annotation 2:** `org.opencontainers.image.version` (The semantic version)

Push command example:

```bash
oras push quay.io/my-org/firmware/cray-bmc:1.10.2 \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  --annotation "dev.fabrica.hardware.compatible=x9000" \
  --annotation "org.opencontainers.image.version=1.10.2" \
  my-firmware.bin:application/vnd.openchami.firmware.payload.v1

```

### Explicit Mode

If firmware binaries are uploaded to the OCI registry using standard tools (like Docker) and lack the exact openchami annotations or artifact types, they can still be utilized. Explicit Mode allows you to bypass the resolution engine by providing the exact OCI repository path and tag (or SHA digest) in your update command.

## 4. Executing Firmware Updates

Updates are triggered by submitting a JSON payload to the service API to create a `FirmwareUpdateJob` resource.

### Targeting Hardware Components

The job specification requires you to identify the hardware component receiving the update. The primary method is to use the `component` field.

When you provide a simple string in the `component` field, the service connects to the target hardware, reads its Firmware Inventory, and automatically discovers the correct Redfish routing URIs by matching your string against the hardware's internal component names or descriptions.

Common `component` values include:

* `"BMC"`
* `"BIOS"`
* `"Chassis"`

*(Advanced) Manual Target Override:* If auto-discovery fails due to non-standard hardware naming conventions, you can omit the `component` field and supply a `targets` array containing the explicit Redfish OData URIs (e.g., `["/redfish/v1/UpdateService/FirmwareInventory/CustomNodeBIOS"]`).

### Example 1: Discovery Mode Update (Auto-Targeting BMC)

This payload instructs the service to query `quay.io/my-org/firmware/cray-bmc`, find the highest semantic version matching the `x9000` hardware model, and apply it. The service will automatically scan the hardware at `10.10.10.50` to find the URI for the "BMC" component.

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "update-bmc-node1"
    },
    "spec": {
      "targetAddress": "10.10.10.50",
      "username": "root",
      "password": "bmc-password",
      "serverProxyAddress": "10.254.1.20",
      "component": "BMC",
      "discovery": {
        "repository": "quay.io/my-org/firmware/cray-bmc",
        "hardwareModel": "x9000",
        "version": "latest"
      }
    }
  }'

```

### Example 2: Explicit Mode Update (Auto-Targeting BIOS)

This payload forces the service to pull a specific OCI reference (`v2.1`) directly, bypassing OCI annotation checks. It instructs the service to automatically discover the routing URI for the "BIOS" component.

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "update-bios-node1"
    },
    "spec": {
      "targetAddress": "10.10.10.50",
      "username": "root",
      "password": "bmc-password",
      "serverProxyAddress": "10.254.1.20",
      "ociReference": "quay.io/my-org/firmware/node-bios:v2.1",
      "component": "BIOS"
    }
  }'

```

## 5. Monitoring and Validation

When a job is successfully created, the POST command will return a JSON object containing a `uid` (e.g., `firmwareupdatejob-8eab5b0e`).

To check the progress, execute a GET request against that UID:

```bash
curl -sS http://127.0.0.1:8090/firmwareupdatejobs/firmwareupdatejob-8eab5b0e

```

The output will display a `status` block indicating the `jobState`. The states progress from `Pending` to `Resolving`, and then to either `InProgress`, `Completed`, or `Failed`. If a job fails, the exact network or Redfish error returned by the target hardware will be recorded in the `errorDetail` field.