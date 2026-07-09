# Campaign Sequencing Handoff

## 1. Implementation Summary

FirmwareUpdateCampaign reconciliation now enforces per-target sequencing for child FirmwareUpdateJob creation.

What changed:

- The campaign reconciler now builds an active target map from existing child jobs.
- A child job is considered active when its JobState is anything other than Completed or Failed, including empty state.
- During desired child job creation, the reconciler skips creation for a target address that already has an active child job.
- After creating a child job, the target address is immediately marked active for that reconciliation pass to prevent multiple new jobs for one target in a single loop.
- Desired job creation order is now deterministic by sorting child keys before creation.

Files changed:

- pkg/reconcilers/firmwareupdatecampaign_reconciler.go
- pkg/reconcilers/firmwareupdatecampaign_reconciler_test.go

## 2. Important Behavior Details

### 2.1 Sequencing semantics

- Scope: Sequencing is per target address inside a single campaign.
- Concurrency limit: At most one active child job per target address per campaign.
- Terminal states: Completed and Failed are terminal and allow the next queued child job to be created.
- Non-terminal states: Empty, Pending, InProgress, and any custom non-terminal value block creation of additional jobs for that same target.

### 2.2 Deterministic order

- Desired jobs are sorted by campaign child key before creation.
- This makes the queue order stable across reconcile runs and reduces nondeterministic scheduling behavior.

### 2.3 Practical implications

- Multi-component campaigns targeting one BMC will no longer create all component jobs at once.
- The first eligible child job is created, subsequent jobs for that target wait for the active one to finish.
- Reconciliation naturally advances the queue as child jobs transition to terminal states.

## 3. Test Coverage Added

Added focused unit tests for the sequencing helpers:

- campaignJobIsActive handles nil, empty, in-progress, completed, and failed states.
- buildActiveCampaignTargets only tracks targets with active jobs.
- reconcileDesiredCampaignJobs now has an integration-style test that validates create-skip-create progression across two reconciliation passes for the same target address.

These tests are in:

- pkg/reconcilers/firmwareupdatecampaign_reconciler_test.go

## 4. How To Use and Validate

1. Create a campaign that generates multiple child jobs for the same target address.
2. Observe that only one new child job is created for that target while an active one exists.
3. Transition the active child job to Completed or Failed.
4. Reconcile again and confirm the next queued child job for that target is created.

Local validation commands used for this change:

- go test ./pkg/reconcilers
- go build ./...

Notes:

- Full go test ./... is still expected to be blocked by known unrelated syntax issues documented in repository memory.
- This sequencing logic is orchestration-side and does not require real hardware to validate basic gating behavior.
