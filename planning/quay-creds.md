### (1) Full Scope and Context

The `firmware-updater` service currently resolves and fetches OCI firmware bundles over unauthenticated connections. To allow the service to pull artifacts from Quay registries, we must introduce an authentication mechanism. Because the service accepts arbitrary OCI references, attaching global credentials to all outbound registry requests poses a security risk of leaking credentials to rogue registries. To mitigate this, the implementation will ingest registry credentials alongside a specific registry domain via environment variables. The ORAS client will be configured to intercept outbound requests and attach the credentials exclusively when the target registry matches the configured domain.

### (2) Code Changes

**Configuration Layer (`cmd/server/main.go`)**
Add three new string fields to the `Config` struct to hold the authentication configuration: `RegistryDomain`, `RegistryUsername`, and `RegistryPassword`. Viper will automatically map these to the `FIRMWARE_UPDATER_REGISTRY_DOMAIN`, `FIRMWARE_UPDATER_REGISTRY_USERNAME`, and `FIRMWARE_UPDATER_REGISTRY_PASSWORD` environment variables. Ensure the default configuration handles empty values safely.

**Resolver Logic Refactor (`pkg/firmwareproxy/resolver.go`)**
The current package-level functions (`ResolvePayload`, `ResolvePayloadFromDiscovery`, `StreamPayloadLayer`) must be converted into methods on a new `Resolver` struct. This struct will hold the configuration values (`domain`, `username`, `password`) injected during instantiation.

**Authentication Client Integration (`pkg/firmwareproxy/resolver.go`)**
Within the newly refactored `Resolver` methods, configure the `remote.Repository` client to use authentication. Use the `oras.land/oras-go/v2/registry/remote/auth` package to create an `auth.Client`. Implement a custom credential function for the `auth.Client` that parses the target registry from the request context, compares it to the configured `RegistryDomain`, and returns the `RegistryUsername` and `RegistryPassword` only if there is an exact match. Apply this client to the `repo.Client` field.

**Route Handler Updates**
Update the HTTP route handlers that currently call the package-level resolver functions (likely within the `registerFirmwareProxyRoute` implementation) to instantiate the `Resolver` struct using the values from the global `Config` object, and execute the operations via the struct methods.

### (3) Acceptance Criteria

* The Go codebase must compile without errors following the structural refactor.
* All unit tests (specifically in `pkg/firmwareproxy/resolver_test.go`) must be updated to instantiate the `Resolver` struct and must pass successfully.
* The system must correctly load the registry configuration values from the respective `FIRMWARE_UPDATER_` environment variables, handling variables starting with special characters like `$`.
* The ORAS authentication client must be proven to withhold credentials when an OCI reference points to a registry domain that does not match the configured domain.
* The service must successfully authenticate and fetch an OCI artifact from a private Quay registry when provided with valid credentials and a matching domain configuration.

### (4) Output Artifacts

## Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-QUAY.md` file in the planning directory containing:

1. A brief summary of the implemented logic.
2. The exact, verified `curl` command that successfully tested the code
3. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.