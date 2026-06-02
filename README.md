# JIT Firmware Execution Service

## 1. Overview and Operational Philosophy

The JIT (Just-In-Time) Firmware Execution Service is a stateless orchestration engine designed to deploy firmware binaries directly from OCI registries to hardware Baseboard Management Controllers (BMCs) using the Redfish standard.

Unlike traditional firmware management tools, this service maintains zero local inventory or catalog tracking of available firmware files. It does not actively crawl or index OCI registries. Instead, it relies on a declarative, on-demand execution model driven entirely by the `FirmwareUpdateJob` resource.

When a job is requested, the service evaluates the OCI reference dynamically, discovers the precise payload locations via the ORAS protocol, registers a localized routing target for the data, and hands off the transfer to the BMC. The service then serves as a transparent proxy, streaming bytes straight from the remote OCI layer to the hardware without writing the binary data to its local disk.

---

## 2. Technical Architecture and Data Flow

The lifecycle of an execution request spans 6 logical stages across the client, the Fabrica server core, the OCI registry, and the physical hardware.

```text
[ Client ] ------------ 1. POST Job ------------> [ Fabrica Server ]
                                                          |
                                                  2. Pull Manifest
                                                          v
                                                 [ OCI Registry ]
                                                          |
                                                  3. Return Digest
                                                          v
[ BMC Hardware ] <----- 4. Redfish SimpleUpdate -- [ Fabrica Server ]
       |
       +--------------- 5. GET Proxy Layer -------------->|
                                                          |
                                                  6. Stream Blob
                                                          v
[ BMC Hardware ] <====== Binary Byte Stream ======= [ OCI Registry ]

```

1. **Job Creation:** An external automation platform or operator issues an HTTP POST containing the target BMC details, credentials, and the explicit OCI image location.
2. **Just-In-Time Resolution:** The background reconciliation loop identifies the new request, sets the internal state to `Resolving`, and connects to the specified OCI registry using `oras-go/v2`.
3. **Manifest Inspection:** The service fetches the OCI manifest layer, confirms the presence of the validation header `application/vnd.openchami.firmware.bundle.v1+json`, and extracts the exact SHA-256 digest of the underlying firmware binary.
4. **Redfish Dispatch:** The service saves a temporary mapping of the SHA-256 digest to its source OCI repository paths. It then triggers an outbound HTTPS POST to the BMC's `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate` route. The payload informs the BMC that its update file is hosted at the service's internal HTTP proxy route: `http://<serverProxyAddress>:8090/firmware-proxy/layer/<digest>`.
5. **Proxy Request:** The BMC processes the instruction and makes an inbound HTTP GET call back to the Fabrica server proxy endpoint to pull down the binary.
6. **Passthrough Streaming:** The proxy endpoint reads the internal map to locate the appropriate upstream OCI repository, initializes a data stream from the registry via ORAS, and flushes the bytes directly into the BMC's HTTP response buffer.

---

## 3. API Specification Contracts

### FirmwareUpdateJob Schema

#### Spec Fields (Input Configuration)

* **`targetAddress`** (string): The IP address or domain name of the destination hardware BMC.
* **`username`** (string): Administrative username for Redfish authentication.
* **`password`** (string): Administrative password for Redfish authentication.
* **`ociReference`** (string): The complete OCI path and tag/digest (e.g., `registry.local:5000/firmware/compute-bmc:1.2.0`).
* **`targets`** (array of strings): Non-empty collection of target Redfish URIs designating the chips or components slated for the update (e.g., `["/redfish/v1/UpdateService/FirmwareInventory/BMC"]`).
* **`serverProxyAddress`** (string): The network reachability address of this Fabrica service instance from the perspective of the BMC network.

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
GOTOOLCHAIN=go1.26.3 go run ./cmd/server serve --port 8090 --database-url="file:readme_test.db?cache=shared&_fk=1" &
SERVER_PID=$!
sleep 3

echo "=== Step 4: Submitting JIT Firmware Update Job ==="
JOB_RESPONSE=$(curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ -H 'Content-Type: application/json' -d '{"metadata":{"name":"readme-jit-job"},"spec":{"targetAddress":"192.0.2.200","username":"admin","password":"password","ociReference":"localhost:5000/firmware/test-bmc:1.0.0","targets":["/redfish/v1/UpdateService/FirmwareInventory/BMC"],"serverProxyAddress":"127.0.0.1"}}')
echo "Server Response: $JOB_RESPONSE"

echo "=== Step 5: Monitoring Asynchronous Reconciliation State ==="
sleep 5
curl -sS http://127.0.0.1:8090/firmwareupdatejobs/readme-jit-job/

echo "=== Step 6: Validating the Just-In-Time Proxy Endpoint ==="
TARGET_DIGEST=$(curl -sS http://127.0.0.1:8090/firmwareupdatejobs/readme-jit-job/ | grep -oE 'sha256:[a-f0-9]{64}' | head -n 1 || echo "sha256:3b1da25e4f135edf46ed0ec00072ab739cbcf26d54922e1957ee14209f179aec")
curl -i http://127.0.0.1:8090/firmware-proxy/layer/$TARGET_DIGEST

echo "=== Step 7: Tearing Down Infrastructure ==="
kill $SERVER_PID
docker rm -f local-oci-registry
rm dummy_firmware.bin readme_test.db
echo "=== Workflow Verification Complete ==="

```

---

## 5. Critical Engineering and Network Constraints

* **Asynchronous API Contracts:** The API returns an immediate `201 Created` code when a job is posted, confirming structural integrity and persistence to the SQLite database. Actual work occurs on background processing threads. Systems driving this API must poll the individual job resource endpoint to capture the definitive status or failure traces.
* **TLS Policy Constraints:** Outbound communication to target hardware addresses defaults exclusively to HTTPS using insecure validation configurations (`InsecureSkipVerify: true`). This accommodation is mandatory to tolerate the self-signed TLS certificates natively generated by bare-metal BMC components in unprovisioned environments.
* **Proxy Address Accuracy:** The `serverProxyAddress` property declared inside the job spec must be accurately routable from the isolated management VLAN hosting the physical BMC. If this address is misconfigured or blocked by intermediate firewalls over port `8090`, the target hardware will timeout during the payload pull phase, causing the overall job status to remain stuck or transition to `Failed`.