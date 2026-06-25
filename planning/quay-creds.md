### (1) Full Scope and Context

The `firmware-updater` service currently interacts with OCI registries to pull firmware bundles anonymously. This behavior causes the service to fail with a `401 Unauthorized` error when attempting to access secured registries, such as Quay, that require authentication to list tags or pull image layers.

The objective is to introduce a single, global configuration for registry authentication using environment variables. These credentials will be read during server startup and utilized by the `firmwareproxy` package to authenticate all outgoing requests to OCI registries. The implementation should rely on the native authentication capabilities of the `oras-go/v2` library, ensuring that operations like tag discovery and blob streaming work correctly against secured endpoints.

### (2) Code Changes

* **Configuration Updates (`cmd/server/main.go`):** * Extend the existing `Config` struct to include fields for registry authentication (e.g., `QuayUsername` and `QuayPassword`).
* Ensure these fields are bound to environment variables following the existing Viper prefix convention (e.g., `FIRMWARE_UPDATER_QUAY_USERNAME` and `FIRMWARE_UPDATER_QUAY_PASSWORD`).


* **Credential Propagation:** * Establish a mechanism to make these credentials available to the `firmwareproxy` package. To minimize architectural shifts, this can be done by introducing a package-level initialization function (e.g., `firmwareproxy.InitAuth(username, password)`) or by injecting a pre-configured authenticated client that the resolver functions can utilize. Choose the simplest path that requires the fewest changes to the existing `ResolvePayload`, `ResolvePayloadFromDiscovery`, and `StreamPayloadLayer` function signatures.
* **ORAS Client Authentication (`pkg/firmwareproxy/resolver.go`):** * Update the logic where `remote.NewRepository(...)` is instantiated.
* Leverage the `oras-go/v2` library to attach an authenticated client to the repository (e.g., configuring `repo.Client` with the appropriate authentication handler using the provided credentials).
* Ensure that the authentication applies to all subsequent ORAS network calls made within the package (`FetchBytes`, `repo.Tags`, `repo.Blobs().Fetch`).



### (3) Acceptance Criteria

* **Compilation:** The codebase must compile cleanly without errors using `go build ./...`.
* **Library Usage:** The authentication must be implemented using standard `oras-go/v2` constructs rather than attempting to manually craft `Authorization` HTTP headers.
* **Local Registry Verification:** Because the target environment (Quay) is unavailable for local testing, the implementation must be validated against a locally hosted OCI registry with basic authentication enabled.
* Stand up a local Docker registry instance configured with `htpasswd` basic auth.
* Push a dummy firmware bundle to this authenticated local registry.
* Configure the `firmware-updater` service with the local credentials via environment variables.
* Trigger a `FirmwareUpdateJob` targeting the local secured repository.


* **Success State:** The job must successfully authenticate, discover tags, and stream the layers without returning a `401 Unauthorized` error.

### (4) Output Artifacts

## Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-PHASE2.md` file in the planning directory containing:

1. A brief summary of the implemented logic.
2. The exact, verified `curl` command that successfully tested the code
3. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.