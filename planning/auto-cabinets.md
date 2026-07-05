# Implementation Plan: Heterogeneous Cabinet Update Reconciler

## 1. Schema and Validation Updates

The `FirmwareUpdateCampaign` resource must be modified to support a "Universal Discovery" mode where specific components and OCI references are omitted in favor of a broad hardware crawl.

### Schema Changes (`pkg/resource/campaign.go`)

* Ensure `Component` and `OCIReference` are pointers (`*string`) or marked with `omitempty` so they are not strictly required.
* Retain the `Discovery` block, as this will store the base OCI repository path for the firmware resolution engine.

### Validation Webhook/Middleware Changes

Update the campaign validation logic to accept the following combinations:

1. **Targeted Mode:** `OCIReference` is set. `Component` is set.
2. **Semi-Targeted Mode:** `Discovery` is set. `Component` is set. (Finds the latest version for *only* the specified component).
3. **Universal Mode (New):** `Discovery` is set. `Component` is **null/empty**. `OCIReference` is **null/empty**. This triggers the heterogeneous crawler.

## 2. Reconciler: Universal Discovery Implementation

When the reconciler processes a campaign in "Universal Mode" (no `Component` specified), it must execute a mapping and resolution routine for every target in the `targets` array before spawning child jobs.

### Phase A: Hardware Inventory Crawl

For each `targetAddress` in the `targets` array:

1. Establish an authenticated Redfish session using the provided `secretID`.
2. Execute an HTTP GET request against `/redfish/v1/UpdateService/FirmwareInventory`.
3. Iterate through the returned `Members` array.
4. For each member URI, execute an HTTP GET request to extract:
* `Id` or `Name` (The component identifier).
* `Version` (The currently installed firmware version).
* `RelatedItem` or hardware identifiers (to map to the `dev.fabrica.hardware.compatible` annotation).



### Phase B: OCI Registry Resolution

For each discovered component on the target:

1. Query the OCI registry defined in `spec.discovery.repository`.
2. Filter the OCI artifacts by matching the hardware identifier to the `dev.fabrica.hardware.compatible` annotation.
3. Extract the `org.opencontainers.image.version` annotation from the matching artifacts.
4. Perform semantic version comparison between the installed version (from Phase A) and the OCI version.
5. If the OCI version is greater than the installed version, flag the component for an update and store the resolved OCI reference string.

### Phase C: Child Job Generation

For every flagged component on the target, construct a `FirmwareUpdateJob` resource.

1. **Job Mapping:** Set the `spec.targetAddress` and `spec.secretID`.
2. **Explicit Targeting:** Because the reconciler has already done the discovery, bypass the child job's auto-discovery logic. Set the `spec.ociReference` to the exact resolved artifact URI, and set the `spec.targets` array (within the child job) to the exact Redfish `@odata.id` URI found in Phase A.
3. **Linkage:** Apply the `campaign-uid` and `campaign-target` annotations for status aggregation.
4. Persist the generated `FirmwareUpdateJob` to the storage backend.

## 3. State Machine and Status Aggregation Fix

The logic governing the `FirmwareUpdateCampaign` status must be updated to account for mixed outcomes across a large batch of child jobs.

Update the aggregation loop to enforce the following state transition rules:

* **Pending:** `summary.total == 0` (Reconciliation has not spawned jobs yet).
* **InProgress:** `summary.pending > 0` (Active work is occurring).
* **Completed:** `summary.pending == 0` AND `summary.completed == summary.total`. (100% success).
* **Failed:** `summary.pending == 0` AND `summary.failed == summary.total`. (100% failure).
* **CompletedWithErrors (New State):** `summary.pending == 0` AND `summary.failed > 0` AND `summary.completed > 0`. (Partial success).

Ensure the `StatusSummary` object accurately increments the `Total` count to reflect the exact number of child jobs spawned across all components and all IPs, rather than just the number of IPs.

### 4. Other Directives

1. **Testing** Ensure that a complete E2E workflow is tested, starting the server and executing a command to ensure correctness
2. **Update documentation** Update the documentation at `docs/user-guide.md` and README.md
2. **Output** Generate a `HANDOFF-CABINETS.md` file in the planning directory containing:
1. A brief summary of the implemented logic.
3. The exact, verified `curl` command that successfully tested the code
4. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.
