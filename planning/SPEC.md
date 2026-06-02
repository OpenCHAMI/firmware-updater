# Autonomous Agent Directive: JIT Firmware Execution Service

## 1. System Overview & Architecture

**Objective:** A stateless execution engine that dynamically pulls OCI firmware payloads on demand and proxies them to hardware BMCs via Redfish.
**Architecture:** This service does NOT track inventory. It exposes a single Custom Resource (`FirmwareUpdateJob`). Upon creation, the service dynamically resolves the OCI reference via ORAS, streams the payload via a custom HTTP proxy route, and orchestrates the Redfish update.

**Fabrica Configuration (MANDATORY):**
You must use the Fabrica framework to scaffold this service.

* **Project Name:** firmware-updater
* **API Group:** hardware.fabrica.dev
* **Storage Type:** ent
* **Database Driver:** sqlite
* **Required Features:** --reconcile, --events

## 2. Resource Schema: FirmwareUpdateJob

Use `fabrica add resource FirmwareUpdateJob` to generate this resource.

**Required Input (Spec):**

* `TargetAddress` (string, required): Hostname or IP of the BMC.
* `Username` (string, required): BMC authentication username.
* `Password` (string, required): BMC authentication password.
* `OCIReference` (string, required): The full OCI registry target (e.g., "registry.example.org/firmware/bmc:1.0.0").
* `Targets` (array of strings, required): Redfish OData URIs to update.
* `ServerProxyAddress` (string, required): Network IP or hostname of this Fabrica server used for the proxy endpoint.

**Required State (Status):**

* `JobState` (string): Current state ("Pending", "Resolving", "InProgress", "Failed", "Completed").
* `TaskID` (string, optional): Redfish Task URI for tracking.
* `ErrorDetail` (string, optional): Exact error message if execution fails.

## 3. Global Execution Requirements

* **Module Dependencies:** Add the required module: `go get oras.land/oras-go/v2`.
* **Proxy Implementation:** In `cmd/server/openapi_extensions.go`, implement a standard Go HTTP route at `/firmware-proxy/layer/{digest}`. This route must parse the requested digest, use the ORAS client (with `PlainHTTP=true` fallback for localhost) to fetch the layer from the OCI registry, and stream the bytes to the HTTP response writer.

## 4. Reconciliation State Machine

Place this logic in `pkg/reconcilers/firmwareupdatejob_reconciler.go`.

* **Idempotency Check:** Halt execution if `JobState` is `InProgress`, `Completed`, or `Failed`.
* **Step 1: OCI Resolution (The JIT Pull):** Update state to `Resolving`. Parse the `OCIReference` to extract the registry, repository, and tag/digest. Initialize an ORAS client, pull the manifest, verify the artifact type is `application/vnd.openchami.firmware.bundle.v1+json`, and extract the digest of the first layer (the payload).
* **Step 2: Redfish Dispatch:** Construct the proxy URI: `http://[ServerProxyAddress]:8090/firmware-proxy/layer/[PayloadDigest]`. Execute an HTTP POST to `https://[TargetAddress]/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate` using insecure TLS. The JSON payload must include the `ImageURI` and the `Targets` array.
* **Error Handling:** Treat HTTP 4xx errors (from ORAS or BMC) as terminal, updating state to `Failed` with the error detail. Treat network timeouts/503s as transient, utilizing an exponential backoff retry. On success, update state to `InProgress`.

## 5. End-to-End Validation (CRITICAL)

You must stage a local OCI registry, push a dummy payload, and physically verify the JIT orchestration loop before handoff.

1. **Stage Registry & Payload:**
`docker run -d -p 5000:5000 --name local-oci-registry registry:2`
`echo "dummy payload" > dummy.bin`
`oras push localhost:5000/firmware/test-bmc:1.0.0 --artifact-type application/vnd.openchami.firmware.bundle.v1+json dummy.bin:application/vnd.openchami.firmware.payload.v1`
2. **Start Server:**
`go run ./cmd/server serve --database-url="file:data.db?cache=shared&_fk=1"`
3. **Trigger JIT Execution:**
Execute a POST to create a `FirmwareUpdateJob` using `localhost:5000/firmware/test-bmc:1.0.0` as the `OCIReference`, and a dummy BMC IP for the `TargetAddress`.
4. **Verify State:**
Execute a GET on the created job. Ensure the job transitions from `Pending` -> `Resolving` -> `Failed` (it will ultimately fail because the dummy BMC IP is unreachable, but the error MUST be a Redfish connection timeout, proving the ORAS payload digest was successfully resolved).

## 6. Output Artifacts

Generate a `HANDOFF.md` containing:

1. A brief summary of the implemented reconciliation logic.
3. The exact, verified `curl` command that successfully created the resource.
4. Detailed notes on important details for using the service, whereby someone with no context could fully utilize the service and it’s endpoints as expected and fully understand the implementation.