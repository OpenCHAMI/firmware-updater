# Service Specification: Firmware Management Service (FMS) - Phase 1

## 1. System Overview & Architecture

**Objective:** A unified service that aggregates metadata from remote OCI registries and orchestrates out-of-band Redfish firmware updates using OCI artifacts.
**Primary Domain:** Hardware Lifecycle / Firmware Management.
**Boundaries:** This service acts as an OCI-to-HTTP proxy and Redfish orchestrator. It does NOT automatically reboot nodes, track complex cross-component dependencies, perform in-band OS-level script updates, or host static binary files on local disk.

**Fabrica Configuration (MANDATORY):**
You must use the Fabrica framework to build this service.

* **Project Name:** firmware-manager
* **API Group:** hardware.fabrica.dev
* **Storage Type:** ent
* **Database Driver:** sqlite
* **Required Features:** --reconcile, --events, --storage

## 2. Business Logic & Resource Schema

### Resource: FirmwareBundle

* **Trigger:** Creation or Update of a `FirmwareBundle` resource.
* **Reconciliation Action:** Validate the format of the `RegistryURL`, `Repository`, and `TagOrDigest` fields. For Phase 1, implement a mock state transition that sets `Status.Discovered` to true and populates `Status.ExtractedMetadata` with test JSON data. Do not implement network calls to the OCI registry in this phase.
* **Required Input (Spec):** * `RegistryURL` (string, required): The domain of the OCI registry (e.g., "registry.example.org").
* `Repository` (string, required): The path to the artifact (e.g., "firmware/hpe/cray-ex-node-bmc").
* `TagOrDigest` (string, required): The OCI tag or sha256 digest (e.g., "v2.14.7").
* `CredentialsSecret` (string, optional): Reference to a secret containing registry auth tokens.


* **Required State (Status):** * `Discovered` (boolean): Indicates if the manifest was successfully parsed.
* `ManifestDigest` (string): The resolved sha256 digest of the manifest.
* `ExtractedMetadata` (map[string]string): Key-value pairs extracted from OCI annotations.
* `Error` (string, optional): Validation or processing errors.



### Resource: FirmwareUpdateJob

* **Trigger:** Creation or Update of a `FirmwareUpdateJob` resource.
* **Reconciliation Action:** Validate the presence of targets and credentials. Verify that `BundleName` references an existing `FirmwareBundle` in the system. Implement idempotency checks to skip execution if `Status.JobState` is already `InProgress`, `Completed`, or `Failed`. For Phase 1, implement a mock state machine transitioning a valid job from `Pending` to `Validating`. Do not implement Redfish network calls in this phase.
* **Required Input (Spec):** * `TargetAddress` (string, required): Hostname or IP of the BMC.
* `Username` (string, required): BMC authentication username.
* `Password` (string, required): BMC authentication password.
* `BundleName` (string, required): Reference to the `metadata.name` of a `FirmwareBundle`.
* `Targets` (array of strings, required): Redfish OData URIs to update.
* `ServerProxyAddress` (string, required): Network IP or hostname of this Fabrica server used for the proxy endpoint.


* **Required State (Status):** * `JobState` (string): Current state ("Pending", "Validating", "InProgress", "Completed", "Failed").
* `TaskID` (string, optional): Redfish Task URI for tracking.
* `ErrorDetail` (string, optional): Exact error message if validation or execution fails.



## 3. Execution & Acceptance Criteria

**Framework Enforcement:** You MUST use the `fabrica` CLI for all project scaffolding and resource generation. DO NOT write a custom Go standard library server, and DO NOT use alternative frameworks 
**Architecture Adherence:** You must place your business logic exclusively inside the Fabrica-generated reconciler stubs (`pkg/reconcilers/`) and use the generated `ent` ORM for state management. Do not bypass the framework's intended patterns.

Execute the framework scaffolding, resource generation, and code implementation to fulfill Sections 1 and 2. You must achieve the following criteria to complete this task:

* **Compilation:** All Go files must compile without errors. Run `go mod tidy` and `go build ./...` after any code modifications. Resolve any compiler errors autonomously.
* **Testing:** Table-driven tests for the custom reconciliation logic must be written and pass via `go test ./...`.
* **Runtime Verification:** The server must successfully bind to the port and route HTTP requests. Verify this locally by starting the server in the background using the required arguments (e.g., `go run ./cmd/server serve --database-url="file:data.db?cache=shared&_fk=1"`).
* **Endpoint Validation:** You must execute a `curl` POST request to the local endpoint to create the generated resource and receive a successful 201 Created HTTP status code. If a 4xx or 5xx code is returned, analyze the logs, correct the implementation, and re-test. You MUST physically test the API by starting the server and sending a request.

## 4. Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF.md` file in the root directory containing:

1. A brief summary of the implemented reconciliation logic.
2. The exact, verified server startup command used during runtime verification.
3. The exact, verified `curl` command that successfully created the resource.
4. Detailed notes on important details for using the service, whereby someone with no context could fully utilize the service and its endpoints as expected and fully understand the implementation.