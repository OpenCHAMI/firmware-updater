# Implementation Directive: Dynamic OCI Search API

## 1. Architectural Objective

Implement a stateless HTTP endpoint that allows operators to discover firmware artifacts in an OCI registry using metadata filters. The service must dynamically query the registry's catalog, evaluate manifest annotations against the user's query parameters, and return the matching artifacts.

Do not generate any new Fabrica Custom Resources (CRDs) or database schemas. This is a pure passthrough execution.

## 2. API Contract

Implement the following HTTP route in `cmd/server/openapi_extensions.go` or an appropriate routing file.

* **Endpoint:** `GET /firmware-search`
* **Inputs:** * `registry` (Query Parameter): The target OCI registry URL (e.g., `127.0.0.1:5000`).
* `[arbitrary_keys]` (Query Parameters): Any additional query parameters must be treated as strict equality filters against the OCI manifest `annotations` map.


* **Outputs (HTTP 200):** A JSON array of objects representing the matches. Each object must contain:
* The full OCI Reference (`registry/repository:tag`).
* The Payload Digest (the SHA-256 digest of the first layer).
* The complete map of extracted annotations.



## 3. Operational Constraints

The agent must adhere to the following system rules during implementation:

* **SDK Usage:** You must use the existing `oras.land/oras-go/v2` SDK to interact with the registry.
* **Artifact Targeting:** The search must only return manifests where the `artifactType` is exactly `application/vnd.openchami.firmware.bundle.v1+json`.
* **Network Fallback:** The ORAS client initialization must support `PlainHTTP` fallback if the target registry is a loopback address (`localhost`, `127.0.0.1`, `::1`).
* **Fault Tolerance:** If a specific repository or tag returns a 404 during the catalog iteration (e.g., a tag was deleted mid-scan), the code must log the error and continue. It must not fail the entire search request. Registry-wide connection failures should return an HTTP 503.

## 4. Acceptance Criteria

You must prove the search works by executing the following terminal commands.

* **Compilation:** All Go files must compile without errors. Run `go mod tidy` and `go build ./...`.
* **Data Staging:** Push two distinct payloads to a local registry.

```bash
oras push 127.0.0.1:5000/firmware/cray-bmc:1.10.2 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json --annotation "vendor=HPE" --annotation "component=bmc" dummy_firmware.bin:application/vnd.openchami.firmware.payload.v1

```

```bash
oras push 127.0.0.1:5000/firmware/dell-bios:2.0.0 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json --annotation "vendor=Dell" --annotation "component=bios" dummy_firmware.bin:application/vnd.openchami.firmware.payload.v1

```

* **Endpoint Validation:** Start the server and query the API for the HPE vendor tag.

```bash
curl -sS "http://127.0.0.1:8090/firmware-search?registry=127.0.0.1:5000&vendor=HPE"

```

The JSON response must include the `cray-bmc:1.10.2` artifact and explicitly omit the `dell-bios:2.0.0` artifact.

## 5. Output Artifacts
Upon meeting all Acceptance Criteria, generate a `HANDOFF-LABELS.md` file in the root directory containing:
1. A brief summary of the implemented logic.
2. The exact, verified server startup command used during runtime verification.
3. The exact, verified `curl` command that triggered the search.
4. Detailed notes on important details for using what was implemented, whereby someone with no context could fully utilize what was implemented as expected and fully understand the implementation.