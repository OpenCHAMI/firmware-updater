## 1. Scope and Context

The objective is to expose the original filename of a pushed firmware binary within the API status of a `FirmwareUpdateJob`. When a firmware payload is pushed to an OCI registry using ORAS, the standard behavior is to attach the filename as an annotation with the key `org.opencontainers.image.title` on the specific layer descriptor.

Currently, the `FirmwareUpdateJob` status tracks the resolved digest and the resolved version. The implementation will extend the OCI manifest parsing logic to extract the title annotation from the payload layer, propagate this value through the discovery result structures, and persist it onto the `FirmwareUpdateJob` status resource during the reconciliation loop.

## 2. Code Changes

### API Type Updates

* Locate the file containing the `v1.FirmwareUpdateJobStatus` struct (likely in `apis/hardware.fabrica.dev/v1/types.go`).
* Add a new string field named `PayloadFilename` to the struct, ensuring it includes the appropriate JSON tags (e.g., `json:"payloadFilename,omitempty"`).

### Firmware Proxy Package Updates

* Locate the `firmwareproxy.DiscoveryResult` struct definition.
* Add a `PayloadFilename string` field to this struct.
* Locate the logic within `firmwareproxy` that fetches and parses the OCI manifest (used by both `ResolvePayload` and `ResolvePayloadFromDiscovery`).
* Update the parsing iteration to inspect the annotations map of the payload layer.
* Extract the string value associated with the `org.opencontainers.image.title` key.
* Assign this extracted string to the new `PayloadFilename` field in the returned results.

### Reconciler Updates

* Modify `reconcileFirmwareUpdateJob.go` to declare a new local string variable `payloadFilename` near the existing `payloadDigest`, `resolvedVersion`, and `resolvedRef` variable declarations.


* Update the block handling `res.Spec.OCIReference` to capture the filename. If `ResolvePayload` is updated to return a struct instead of a single string, update the assignment signature accordingly to extract the filename.


* Update the block handling `res.Spec.Discovery` to assign `payloadFilename = resolved.PayloadFilename` alongside the existing assignments for digest, version, and reference.


* Assign the local variable to the resource status by adding `res.Status.PayloadFilename = payloadFilename` directly after the existing assignments for `res.Status.ResolvedDigest` and `res.Status.ResolvedVersion`.



## 3. Acceptance Criteria

* The code compiles successfully with no syntax or type errors.
* The OCI resolver safely handles manifests where the `org.opencontainers.image.title` annotation is missing, leaving the `PayloadFilename` field empty rather than causing a panic.
* End-to-end testing must be performed by executing the server and observing the status outputs.
* **Test Execution 1: Pushing and Serving**
* Start the server with the standard command using a local SQLite database.
* Execute an `oras push` command providing a local file, such as: `oras push 127.0.0.1:5000/firmware/17:3.0.0 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json --annotation "org.opencontainers.image.version=3.0.0" --annotation "dev.fabrica.hardware.compatible=Embedded Video Controller,102b0538159000e4" ./dummy-video.bin:application/octet-stream`.


* **Test Execution 2: Campaign Submission and Verification**
* Submit a test campaign using `curl -sS -X POST http://127.0.0.1:8090/firmwareupdatecampaigns` targeting a mock component that matches the pushed artifact.
* Execute `curl -sS http://127.0.0.1:8090/firmwareupdatejobs/ | jq` to view the generated child jobs.
* Verify that the JSON output for the job status includes the `payloadFilename` field and that its value strictly equals `"dummy-video.bin"`.



---

## Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-PHASE2.md` file in the planning directory containing:

1. A brief summary of the implemented logic.
2. An explanation of what tests were run and how it could be tested by an individual.
3. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.