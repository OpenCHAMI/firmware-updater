# Phase 2: Reconciliation Implementation - Firmware Management Service

## 1. Context Acquisition

Read the `HANDOFF.md` file in the root directory to understand the existing schema for `Spec` and `Status`. Do not modify the underlying database driver or storage types.

## 2. Global Execution Requirements

* **Module Dependencies:** Add the required OCI registry client module by executing the command `go get oras.land/oras-go/v2`.
* **Proxy Implementation:** In `cmd/server/openapi_extensions.go`, implement a standard Go HTTP route at `/firmware-proxy/layer/{digest}`. This route must parse the requested digest, use the ORAS client to fetch the corresponding layer from the OCI registry, and stream the bytes directly to the HTTP response writer.

## 3. FirmwareBundle Reconciliation State Machine

* **Pre-flight Checks:** Validate that the OCI registry target format is correct based on `RegistryURL`, `Repository`, and `TagOrDigest`.
* **Execution Steps:** Initialize an `oras-go/v2` client using the provided registry details. Pull the OCI manifest from the remote registry. Verify that the manifest artifact type is exactly `application/vnd.openchami.firmware.bundle.v1+json`. Parse the manifest annotations and extract the metadata into a key-value map.
* **Error Handling:** Treat HTTP 401, 403, and 404 from the registry, or an invalid artifact type, as terminal errors. Treat network timeouts (HTTP 503/504) as transient errors. Implement a retry backoff strategy for transient errors starting at 5 seconds, up to a maximum of 5 attempts.

## 4. FirmwareBundle State Updates

* **On Success:** Set `Status.Discovered = true`, populate `Status.ExtractedMetadata` with the parsed annotations, and record the `Status.ManifestDigest`.
* **On Transient Failure:** Leave `Status.Discovered = false` and append the network timeout error message to `Status.Error`.
* **On Terminal Failure:** Set `Status.Discovered = false`, record the exact failure reason in `Status.Error`, and halt further reconciliation.

## 5. FirmwareUpdateJob Reconciliation State Machine

* **Pre-flight Checks:** Verify that the referenced `BundleName` exists in the local database. Ensure the job is not already in a terminal state (Completed, Failed) or actively running (InProgress).
* **Execution Steps:** Retrieve the payload digest from the associated `FirmwareBundle`. Construct the proxy URI using the pattern `http://[ServerProxyAddress]:8090/firmware-proxy/layer/[PayloadDigest]`. Execute an HTTP POST to `https://[TargetAddress]/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate` using insecure TLS. The JSON payload must include the `ImageURI` and the `Targets` array.
* **Error Handling:** Treat HTTP 400 (Bad Request) from the BMC or an unreachable network host as terminal errors. Treat HTTP 503 (Service Unavailable) from the BMC as a transient error. Implement a retry backoff strategy for transient errors starting at 10 seconds, up to a maximum of 3 attempts.

## 6. FirmwareUpdateJob State Updates

* **On Success:** Set `Status.JobState = "InProgress"` and extract the Redfish `TaskID` from the response headers or body if provided.
* **On Transient Failure:** Set `Status.JobState = "Validating"` and append the BMC timeout message to `Status.ErrorDetail`.
* **On Terminal Failure:** Set `Status.JobState = "Failed"`, populate `Status.ErrorDetail` with the exact Redfish error, and halt further execution.

## 7. Acceptance Criteria

* **Compilation:** The code must compile. Run the command `go mod tidy && go build ./...` after modifications.
* **Testing:** Write specific unit tests targeting the new error handling and state transitions in both reconcilers. Run the command `go test ./...`.
* **Idempotency Verification:** The reconciler must be idempotent. It should be able to run multiple times against the same `Spec` without duplicating external resources or throwing state errors.

## 8. Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-PHASE2.md` file in the root directory containing:

1. A brief summary of the implemented reconciliation logic.
2. The exact, verified server startup command used during runtime verification.
3. The exact, verified `curl` command that successfully created the resource.
4. Detailed notes on important details for using the service, whereby someone with no context could fully utilize the service and its endpoints as expected and fully understand the implementation.