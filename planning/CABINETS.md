## Implementation Plan: FirmwareUpdateCampaign Resource

The following details the implementation of a bulk update feature using the Fabrica framework, mapping out the user interaction, the necessary code modifications, and the structural definitions.

### 1. User Workflow

The end-to-end process for a systems administrator executing a cabinet-level update.

1. **Target Acquisition:** The user queries their inventory system (e.g., SMD) to retrieve an array of BMC IP addresses for the target cabinet.
2. **Payload Assembly:** The user constructs a JSON document representing a `FirmwareUpdateCampaign`. This document defines the shared discovery parameters and the specific array of hardware targets.
3. **Submission:** The user submits a POST request containing the JSON payload to the `/firmwareupdatecampaigns/` endpoint.
4. **UID Receipt:** The service returns a 201 Created status along with the assigned UID (e.g., `campaign-1a2b3c4d`).
5. **Monitoring:** The user executes a GET request against `/firmwareupdatecampaigns/campaign-1a2b3c4d`. The returned status block provides an aggregate count (total, completed, failed, pending) and an array of individual child job states.

---

### 2. Code Update Flow (Fabrica Integration)

Because the service uses Fabrica, the implementation relies heavily on defining the schema and allowing the code generator to build the CRUD boilerplate.

#### Step 2.1: Resource Definition

Create a new file in your resource definition package (e.g., `pkg/resource/campaign.go`). Define the structures for the Campaign, its Spec, and its Status.

```go
package resource

// FirmwareUpdateCampaign represents a bulk update operation.
type FirmwareUpdateCampaign struct {
    APIVersion string                       `json:"apiVersion"`
    Kind       string                       `json:"kind"`
    Metadata   Metadata                     `json:"metadata"`
    Spec       FirmwareUpdateCampaignSpec   `json:"spec"`
    Status     FirmwareUpdateCampaignStatus `json:"status,omitempty"`
}

type FirmwareUpdateCampaignSpec struct {
    ServerProxyAddress string            `json:"serverProxyAddress"`
    Component          string            `json:"component,omitempty"`
    Discovery          *DiscoverySpec    `json:"discovery,omitempty"`
    OCIReference       string            `json:"ociReference,omitempty"`
    Targets            []CampaignTarget  `json:"targets"`
}

type CampaignTarget struct {
    TargetAddress string `json:"targetAddress"`
    SecretID      string `json:"secretID"`
}

type FirmwareUpdateCampaignStatus struct {
    CampaignState string        `json:"campaignState"`
    Summary       StatusSummary `json:"summary"`
    ChildJobs     []ChildJob    `json:"childJobs"`
}

type StatusSummary struct {
    Total     int `json:"total"`
    Completed int `json:"completed"`
    Failed    int `json:"failed"`
    Pending   int `json:"pending"`
}

type ChildJob struct {
    TargetAddress string `json:"targetAddress"`
    JobUID        string `json:"jobUID"`
    JobState      string `json:"jobState"`
    ErrorDetail   string `json:"errorDetail,omitempty"`
}

```

#### Step 2.2: Code Generation

Run the Fabrica code generator against the new resource definition. This will automatically generate:

* REST API handlers for `/firmwareupdatecampaigns` (Create, Read, Update, Delete, List).
* Storage backend wrappers for saving and loading the campaign.
* Validation scaffolding.

#### Step 2.3: Business Logic Implementation (The Reconciler)

Fabrica generates the CRUD layer, but custom logic is required to translate a newly created `FirmwareUpdateCampaign` into individual `FirmwareUpdateJob` resources.

You must implement a background worker or intercept the generated Create handler. The background worker (reconciliation loop) is the standard approach for Kubernetes-style resources.

1. **Watch/Poll:** A routine polls the storage backend for `FirmwareUpdateCampaign` resources where `Status.CampaignState` is empty or `Pending`. Polling intervals should be configured between 5 to 15 seconds to minimize disk I/O while remaining responsive.
2. **Job Spawning:** For a detected campaign, the worker iterates over the `Spec.Targets` array. For each target, it creates a new `FirmwareUpdateJob` struct in memory, copying the shared `ServerProxyAddress`, `Component`, and `Discovery` parameters, and appending the specific `TargetAddress`.
3. **Storage Commit:** The worker submits these new jobs to the `FirmwareUpdateJob` storage backend.
4. **Status Aggregation:** A secondary routine polls campaigns in the `InProgress` state. It queries the storage backend for all `FirmwareUpdateJob` resources associated with the campaign's targets. It tallies the states, updates the `StatusSummary` counts, populates the `ChildJobs` array, and saves the updated campaign object back to storage.

---

### 3. Execution Constraints and Concurrency

When submitting bulk operations, the downstream hardware controllers and the OCI registry are subject to load constraints.

* **Batch Sizing:** A standard dense cabinet contains 64 to 128 nodes. Ensure the routine that spawns child jobs does not saturate the internal event bus or the underlying storage backend. Submitting up to 150 jobs concurrently is typically safe for local storage, but network saturation must be evaluated.
* **Proxy Load:** The `serverProxyAddress` will be handling simultaneous firmware downloads from multiple BMCs. The proxy configuration must support concurrent inbound connections equal to the maximum expected nodes in a cabinet (e.g., configuring `MaxClients` or equivalent worker threads to at least 150).
* **UID Tracking:** Ensure the generator creates a deterministic linkage between the parent campaign and the child jobs. The child `FirmwareUpdateJob` should include an annotation or label (e.g., `campaign-uid: campaign-1a2b3c4d`) to allow the aggregation routine to efficiently query only the relevant jobs during status updates.

---

### 4. Other Directives

1. **Testing** Ensure that a complete E2E workflow is tested, starting the server and executing a command to ensure correctness
2. **Update documentation** Update the documentation at `docs/user-guide.md` and README.md
2. **Output** Generate a `HANDOFF-CABINETS.md` file in the planning directory containing:
1. A brief summary of the implemented logic.
3. The exact, verified `curl` command that successfully tested the code
4. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.