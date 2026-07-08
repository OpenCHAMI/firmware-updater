(1) Scope and Context

Baseboard Management Controllers (BMCs) typically possess limited processing and memory resources. When multiple firmware update payloads are dispatched to a single BMC concurrently, it can result in race conditions, dropped requests, or hardware lockups. To prevent this, firmware update tasks directed at the same physical hardware must be executed sequentially.

The strategy implements a throttling mechanism within the `FirmwareUpdateCampaign` reconciliation loop. Instead of spawning all required `FirmwareUpdateJob` resources simultaneously, the campaign reconciler will evaluate the current execution state of child jobs mapped to each `TargetAddress`. If a specific `TargetAddress` has 1 or more active jobs (any state other than `Completed` or `Failed`), the reconciler will pause the instantiation of subsequent jobs for that target. Once the active job reaches a terminal state, the next pending job in the queue for that target is created. This ensures a concurrency limit of 1 active update job per IP address per campaign.

(2) Code Changes

The modifications will be isolated to `pkg/reconcilers/firmwareupdatecampaign_reconciler.go`, specifically within the `reconcileFirmwareUpdateCampaign` function.

1. **Identify Active Targets:** Before iterating through `desiredJobs`, initialize a tracking map (e.g., `activeTargets := make(map[string]bool)`). Iterate through the existing `jobsByKey` map. For each existing job, inspect `job.Status.JobState`. If the state is not `v1.CampaignStateCompleted` and not `v1.CampaignStateFailed` (or if it is empty/pending), extract the target address using the existing `campaignChildTargetAddress(job)` helper and set `activeTargets[targetAddress] = true`.
2. **Gate Job Creation:**
During the loop iterating over `desiredJobs`, retrieve the target address for the pending job using `campaignChildTargetAddress(desired.job)`. Check if this target address exists in the `activeTargets` map.
* If `activeTargets[targetAddress]` is `true`, execute a `continue` statement to skip creation of this job during the current reconciliation cycle.
* If it is `false` (or missing), proceed with the standard UID generation and client creation logic.


3. **Update Active Targets Map:**
Immediately after successfully creating a new `FirmwareUpdateJob` using `r.Client.Create(ctx, job)`, set `activeTargets[targetAddress] = true`. This prevents the reconciler from creating multiple new jobs for the same target within a single execution loop.
4. **Enforce Deterministic Ordering (Optional but Recommended):**
To ensure the sequence of updates is predictable across reconciliation runs, verify that the `desiredJobs` slice is deterministically ordered (e.g., sorted alphabetically by component identifier or URI) before the job creation loop begins.

(3) Acceptance Criteria

1. The Go code compiles without errors (`go build ./...`).
2. The application server initializes successfully locally.
3. Some form of testing to ensure that things are working as expected to the best of your ability in this environment where there is no real hardware accessible for testing. A mock BMC or something perhaps.

(4) Output Artifacts

## Output Artifacts

Upon meeting all Acceptance Criteria, generate a `HANDOFF-campaign-sequencing.md` file in the planning directory containing:

1. A brief summary of the implemented logic.
2. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.