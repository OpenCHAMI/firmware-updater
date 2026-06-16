The Fabrica documentation states that all automatically generated files end with the `_generated.go` suffix. When you run `fabrica generate` after modifying a resource (like updating a `Spec` or `Status` field), the generator overwrites these specific `_generated.go` files to match the new schema.

However, it will not overwrite your custom logic. The documentation designates `cmd/server/openapi_extensions.go` as a "**create-once**, never overwritten" hook designed specifically for custom, non-generated routes. Because the `GET /firmware-search` route will be registered in `openapi_extensions.go` and the business logic placed into a new custom file (`pkg/firmwareproxy/search.go`), your code is safe from regeneration. Furthermore, since this search endpoint does not modify or create a new Fabrica resource, running `fabrica generate` is not required for this specific addition.

Here is the implementation plan utilizing your exact formatting structure.

# Phase 2: Reconciliation Implementation - Dynamic OCI Search Endpoint

## 1. Context Acquisition

Read the `pkg/firmwareproxy/resolver.go` file to understand how the service initializes `oras.land/oras-go/v2` remote registry clients and handles `PlainHTTP` fallback for loopback addresses. Do not modify the underlying database driver, storage types, or generate any new Fabrica Custom Resources (CRDs). The routing logic must be placed in `cmd/server/openapi_extensions.go`.

## 2. Reconciliation State Machine

Implement the following stateless logic inside `pkg/firmwareproxy/search.go`.

* **Pre-flight Checks:**
* Parse the target registry URL provided by the caller in the `registry` query parameter. Determine if `PlainHTTP` must be enabled (e.g., if the target is `localhost`, `127.0.0.1`, or `::1`).


* **Execution Steps:**
* Step 1: Initialize a `remote.Registry` client pointing to the target registry.
* Step 2: Execute `registry.Repositories(ctx)` to retrieve all repositories.
* Step 3: Iterate over each repository. Initialize a `remote.Repository` client and execute `repo.Tags(ctx)` to retrieve all tags.
* Step 4: Iterate over each tag. Use `oras.FetchBytes(ctx, ...)` to pull the manifest for `repository:tag`.
* Step 5: Unmarshal the manifest bytes and verify `artifactType` exactly matches `application/vnd.openchami.firmware.bundle.v1+json`.
* Step 6: Iterate through the user's provided search parameters. Verify that every provided key exists in the manifest's `annotations` map and that the values match exactly.
* Step 7: Append successful matches to a result slice containing the OCI Reference (`registry/repo:tag`), the Payload Digest (digest of the first layer), and the full annotations map.


* **Error Handling:**
* Item-level 404 errors during the repository/tag iteration loop (e.g., a tag was deleted during the scan) are transient. Log the error and `continue` to the next iteration.
* Registry connection timeouts or 5xx errors are terminal. Halt execution and return the error.



## 3. State Updates

Based on the execution steps, update the HTTP response explicitly in `cmd/server/openapi_extensions.go`.

* **On Success:** Set the `Content-Type` header to `application/json`, return an HTTP 200 status code, and write the JSON-marshaled result slice.
* **On Transient Failure:** (Handled internally by the iteration loop; skipped items do not affect the final 200 OK state).
* **On Terminal Failure:** Return an HTTP 503 status code and append the error message indicating the OCI backend is unavailable.

## 4. Acceptance Criteria

* **Compilation:** The code must compile. Run `go mod tidy` and `go build ./...`.
* **Testing:** Stage two distinct payloads in a local registry using ORAS:
`oras push 127.0.0.1:5000/firmware/cray-bmc:1.10.2 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json --annotation "vendor=HPE" --annotation "component=bmc" dummy_firmware.bin:application/vnd.openchami.firmware.payload.v1`
`oras push 127.0.0.1:5000/firmware/dell-bios:2.0.0 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json --annotation "vendor=Dell" --annotation "component=bios" dummy_firmware.bin:application/vnd.openchami.firmware.payload.v1`
* **Idempotency Verification:** The endpoint must be able to run multiple times against the same query without duplicating the returned results. Execute `curl -sS "http://127.0.0.1:8090/firmware-search?registry=127.0.0.1:5000&vendor=HPE"`. The JSON response must include the `cray-bmc:1.10.2` artifact and explicitly omit the `dell-bios:2.0.0` artifact.

## 5. Output Artifacts

Generate a `CHASSIS_HANDOFF.md` containing:

1. A brief summary of the implemented logic.
3. The exact, verified `curl` command that successfully executes your code.
4. Detailed notes on important details for using the code, whereby someone with no context could fully utilize the code you implemented and it’s endpoints as expected and fully understand the implementation.