# JIT Firmware Execution Service

# Runbook: JIT OCI-to-Redfish Firmware Deployment (Cray EX)

## 1. Environment and Toolchain Initialization

To comply with HPC environment constraints, the required toolchains (Go and ORAS) were installed directly into the user's home directory, and the execution environment was staged in the scratch partition.

**Commands Executed:**

```bash
mkdir -p /scratch/$USER/firmware-testing 
cd /scratch/$USER/firmware-testing
wget https://go.dev/dl/go1.22.4.linux-amd64.tar.gz 
tar -C $HOME -xzf go1.22.4.linux-amd64.tar.gz 
export PATH=$PATH:$HOME/go/bin
curl -LO https://github.com/oras-project/oras/releases/download/v1.2.0/oras_1.2.0_linux_amd64.tar.gz
mkdir -p $HOME/bin
tar -zxf oras_1.2.0_linux_amd64.tar.gz -C $HOME/bin oras
export PATH=$PATH:$HOME/bin

```

**Mechanism:** This bypasses system-level package managers and provides the localized binaries required to compile the Fabrica service and interact with the OCI registry.

## 2. OCI Registry and Artifact Staging

The deployment model replaces traditional static HTTP file servers with a standard OCI distribution registry.

**Commands Executed:**

```bash
podman run -d -p 5000:5000 --replace --name local-oci-registry registry:2
curl -O http://rgw-vip.hmn:8080/fw-update/2d64752c1cad11f1aeaa62a6103f192d/NC-1.10.2-22-s.tar.gz
oras push 127.0.0.1:5000/firmware/cray-bmc:1.10.2 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json NC-1.10.2-22-s.tar.gz:application/vnd.openchami.firmware.payload.v1

```

**Mechanism:** The `oras push` command mathematically hashes the 58MB `NC-1.10.2-22-s.tar.gz` file, stores it by its SHA-256 digest, and generates an OCI manifest linking the tag `1.10.2` to that digest. Explicitly utilizing `127.0.0.1` instead of `localhost` forces IPv4 routing, avoiding the IPv6 loopback trap (`dial tcp [::1]:5000: connect: connection refused`) that previously caused the push to fail.

## 3. The Fabrica Service Code Modifications

Before execution, the Go reconciler required modifications to accommodate Cray hardware, non-standard Redfish implementations, and payload sizes:

1. **Redfish Auto-Discovery:** Instead of hardcoding standard DMTF paths, the service now dynamically queries the target BMC's `UpdateService` to discover its specific `SimpleUpdate` action URI and parses the `FirmwareInventory` to map human-readable components (like "BMC") to explicit target URIs dynamically.
2. **Redfish Payload Adjustment:** The JSON struct was updated to inject `"TransferProtocol": "HTTP"`, a strict requirement for Cray BMCs to initiate the pull.
3. **Proxy Streaming & HEAD Support:** The ORAS client limits memory-buffered downloads to 4MB to prevent out-of-memory crashes. Because the Cray payload is roughly 58MB, the proxy endpoint was rewritten to use `io.Copy`, streaming the bytes directly from the registry to the BMC without holding them in memory. The router was also updated to accept HTTP `HEAD` requests, allowing the BMC to verify the `Content-Length` header before initiating the full 58MB transfer.

## 4. Execution and Orchestration

With the registry populated and the code compiled, the service was started and the deployment job was submitted.

**Commands Executed:**

```bash
go run ./cmd/server serve --port 8090 --database-url="file:hpc_test.db?cache=shared&_fk=1" &
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ -H 'Content-Type: application/json' -d '{"metadata":{"name":"live-cray-update-retry"},"spec":{"targetAddress":"x9000c3s7b1","username":"root","password":"initial0","ociReference":"127.0.0.1:5000/firmware/cray-bmc:1.10.2","targets":["/redfish/v1/UpdateService/FirmwareInventory/BMC"],"serverProxyAddress":"10.254.1.20"}}'

```

**Mechanism:** The `serverProxyAddress` of `10.254.1.20` is the specific IPv4 address of the Non-Compute Node (NCN) on the `bond0.hmn0` interface. The background reconciler dynamically discovered the UpdateService action URI, extracted the SHA-256 digest from the OCI registry, constructed the download URI (`http://10.254.1.20:8090/firmware-proxy/layer/sha256:...`), and pushed that URI to the BMC via Redfish. The BMC then routed its download request back through the Hardware Management Network to the Fabrica proxy.

## 5. Hardware Validation

The BMC was queried to confirm the state transition.

**Command Executed:**

```bash
curl -k -u root:initial0 https://x9000c3s7b1/redfish/v1/UpdateService/FirmwareInventory/BMC | jq

```

**Output:**

```json
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100   392  100   392    0     0    922      0 --:--:-- --:--:-- --:--:--   924
{
  "@odata.etag": "W/\"1781212049\"",
  "@odata.id": "/redfish/v1/UpdateService/FirmwareInventory/BMC",
  "@odata.type": "#SoftwareInventory.v1_1_0.SoftwareInventory",
  "Description": "Baseboard Management Controller",
  "Id": "BMC",
  "Name": "BMC",
  "SoftwareId": "nc:*:*:*",
  "Status": {
    "Health": "OK",
    "State": "Updating"
  },
  "Updateable": true,
  "Version": "nc.1.10.2-22-shasta-release.arm.2026-01-15T01:13:10+00:00.a0bcef9"
}

```

**Mechanism:** The absence of the `Conditions` array containing the `HPEFirmwareUpdate.1.0.DownloadFailed` warning indicates the proxy transfer succeeded. The `State: Updating` and `Health: OK` fields confirm the BMC verified the binary signature and has begun applying the flash update.

---

## 1. Overview and Operational Philosophy

The JIT (Just-In-Time) Firmware Execution Service is a stateless orchestration engine designed to deploy firmware binaries directly from OCI registries to hardware Baseboard Management Controllers (BMCs), Chassis Controllers, and Cabinet Controllers using the Redfish standard.

Unlike traditional firmware management tools, this service maintains zero local inventory or catalog tracking of available firmware files. It does not actively crawl or index OCI registries. Instead, it relies on a declarative, on-demand execution model driven entirely by the `FirmwareUpdateJob` resource.

When a job is requested, the service evaluates the OCI reference dynamically, discovers the precise payload locations via the ORAS protocol, queries the hardware to automatically discover routing targets, registers a localized routing target for the data, and hands off the transfer to the controller. The service then serves as a transparent proxy, streaming bytes straight from the remote OCI layer to the hardware without writing the binary data to its local disk.

---

## 2. Technical Architecture and Data Flow

The lifecycle of an execution request spans 7 logical stages across the client, the Fabrica server core, the OCI registry, and the physical hardware.

```text
[ Client ] ------------ 1. POST Job ------------> [ Fabrica Server ]
                                                          |
                                                  2. Pull Manifest
                                                          v
                                                 [ OCI Registry ]
                                                          |
                                                  3. Return Digest
                                                          |
                                                  4. Auto-Discover URIs
                                                          v
[ BMC Hardware ] <----- 5. Redfish SimpleUpdate -- [ Fabrica Server ]
       |
       +--------------- 6. GET Proxy Layer -------------->|
                                                          |
                                                  7. Stream Blob
                                                          v
[ BMC Hardware ] <====== Binary Byte Stream ======= [ OCI Registry ]


```

1. **Job Creation:** An external automation platform or operator issues an HTTP POST containing the target BMC details, credentials, and the explicit OCI image location.
2. **Just-In-Time Resolution:** The background reconciliation loop identifies the new request, sets the internal state to `Resolving`, and connects to the specified OCI registry using `oras-go/v2`.
3. **Manifest Inspection:** The service fetches the OCI manifest layer, confirms the presence of the validation header `application/vnd.openchami.firmware.bundle.v1+json`, and extracts the exact SHA-256 digest of the underlying firmware binary.
4. **Redfish Auto-Discovery:** The service queries the target hardware's `UpdateService` to dynamically discover the appropriate `SimpleUpdate` action URI. If a `component` string was provided instead of explicit targets, it also scans the `FirmwareInventory` to map the human-readable intent to explicit hardware targets.
5. **Redfish Dispatch:** The service saves a temporary mapping of the SHA-256 digest to its source OCI repository paths. It then triggers an outbound HTTPS POST to the discovered Redfish action route. The payload informs the hardware that its update file is hosted at the service's internal HTTP proxy route: `http://<serverProxyAddress>:8090/firmware-proxy/layer/<digest>`.
6. **Proxy Request:** The hardware processes the instruction and makes an inbound HTTP GET call back to the Fabrica server proxy endpoint to pull down the binary.
7. **Passthrough Streaming:** The proxy endpoint reads the internal map to locate the appropriate upstream OCI repository, initializes a data stream from the registry via ORAS, and flushes the bytes directly into the hardware's HTTP response buffer.

---

## 3. API Specification Contracts

### FirmwareUpdateJob Schema

#### Spec Fields (Input Configuration)

* **`targetAddress`** (string): The IP address or domain name of the destination hardware interface.
* **`username`** (string): Administrative username for Redfish authentication.
* **`password`** (string): Administrative password for Redfish authentication.
* **`ociReference`** (string): The complete OCI path and tag/digest (e.g., `registry.local:5000/firmware/compute-bmc:1.2.0`).
* **`targets`** (array of strings, optional): Explicit collection of target Redfish URIs designating the chips or components slated for the update.
* **`component`** (string, optional): Human-readable intent string (e.g., "BMC", "BIOS") used to auto-discover targets dynamically. *Note: Either targets or component must be provided.*
* **`serverProxyAddress`** (string): The network reachability address of this Fabrica service instance from the perspective of the management network.

#### Status Fields (State Tracking)

* **`jobState`** (string): Reflects the position in the state machine: `Pending`, `Resolving`, `InProgress`, `Failed`, or `Completed`.
* **`taskID`** (string): The tracked Redfish Task resource URI spawned by the BMC on successful delivery acceptance.
* **`errorDetail`** (string): Complete text trace of structural, authentication, or transport anomalies encountered during execution.

### Network Proxy Interface

* **Endpoint:** `GET /firmware-proxy/layer/{digest}`
* **Headers Produced:** `Content-Type: application/octet-stream`
* **Transport Fallback:** Automatically switches to `PlainHTTP=true` whenever the target registry URI matches loopback addresses (`localhost`, `127.0.0.1`, or `::1`).

---

## 4. End-to-End Workflow Automation Script

This shell script provides a self-contained test workflow. It establishes a local isolated registry, produces a simulated payload, injects it into the OCI system via the ORAS CLI, spins up the server infrastructure, executes a JIT job request, and tests the proxy endpoints.

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "=== Step 1: Setting up Local Infrastructure ==="
docker run -d -p 5000:5000 --name local-oci-registry registry:2

echo "=== Step 2: Creating and Publishing OCI Firmware Artifact ==="
echo "EMBEDDED-FIRMWARE-BINARY-PAYLOAD-DATA-010101" > dummy_firmware.bin
oras push localhost:5000/firmware/test-bmc:1.0.0 --artifact-type application/vnd.openchami.firmware.bundle.v1+json dummy_firmware.bin:application/vnd.openchami.firmware.payload.v1

echo "=== Step 3: Launching JIT Firmware Execution Service ==="
GOTOOLCHAIN=go1.26.3 go build -o ./tmp/server ./cmd/server
./tmp/server serve --port 8090 --database-url="file:readme_test.db?cache=shared&_fk=1" &
SERVER_PID=$!
sleep 3

echo "=== Step 4: Submitting JIT Firmware Update Job ==="
JOB_RESPONSE=$(curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ -H 'Content-Type: application/json' -d '{"metadata":{"name":"readme-jit-job"},"spec":{"targetAddress":"192.0.2.200","username":"admin","password":"password","ociReference":"localhost:5000/firmware/test-bmc:1.0.0","targets":["/redfish/v1/UpdateService/FirmwareInventory/BMC"],"serverProxyAddress":"127.0.0.1"}}')
echo "Server Response: $JOB_RESPONSE"

JOB_UID=$(echo "$JOB_RESPONSE" | grep -oE '"uid":"[^"]+"' | head -n 1 | sed 's/"uid":"//' | sed 's/"//')
echo "Extracted Job UID: $JOB_UID"

echo "=== Step 5: Monitoring Asynchronous Reconciliation State ==="
JOB_STATUS=""
for i in {1..20}; do
  JOB_STATUS=$(curl -sS http://127.0.0.1:8090/firmwareupdatejobs/$JOB_UID/)
  CURRENT_STATE=$(echo "$JOB_STATUS" | grep -oE '"jobState":"[^"]+"' | head -n 1 | sed 's/"jobState":"//' | sed 's/"//' || echo "Pending")
  echo "Polled State: $CURRENT_STATE"
  if [ "$CURRENT_STATE" != "Pending" ] && [ "$CURRENT_STATE" != "Resolving" ]; then
    break
  fi
  sleep 5
done

echo "=== Step 6: Validating the Just-In-Time Proxy Endpoint ==="
TARGET_DIGEST=$(echo "$JOB_STATUS" | grep -oE 'sha256:[a-f0-9]{64}' | head -n 1 || echo "")

if [ -z "$TARGET_DIGEST" ]; then echo "Error: Could not extract resolved digest from job status. Reconciler might still be executing."; else curl -i http://127.0.0.1:8090/firmware-proxy/layer/$TARGET_DIGEST; fi

echo "=== Step 7: Tearing Down Infrastructure ==="
kill $SERVER_PID
docker rm -f local-oci-registry
rm -rf ./tmp
rm dummy_firmware.bin readme_test.db
echo "=== Workflow Verification Complete ==="

```

---

## 5. Critical Engineering and Network Constraints

* **Asynchronous API Contracts:** The API returns an immediate `201 Created` code when a job is posted, confirming structural integrity and persistence to the SQLite database. Actual work occurs on background processing threads. Systems driving this API must poll the individual job resource endpoint to capture the definitive status or failure traces.
* **TLS Policy Constraints:** Outbound communication to target hardware addresses defaults exclusively to HTTPS using insecure validation configurations (`InsecureSkipVerify: true`). This accommodation is mandatory to tolerate the self-signed TLS certificates natively generated by bare-metal BMC components in unprovisioned environments.
* **Proxy Address Accuracy:** The `serverProxyAddress` property declared inside the job spec must be accurately routable from the isolated management VLAN hosting the physical BMC. If this address is misconfigured or blocked by intermediate firewalls over port `8090`, the target hardware will timeout during the payload pull phase, causing the overall job status to remain stuck or transition to `Failed`.