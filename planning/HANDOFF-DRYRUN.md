# Dry Run Implementation Handoff

## 1. Summary

Implemented dry-run behavior for both FirmwareUpdateCampaign and FirmwareUpdateJob.

When dryrun is true:
- Campaign-generated child jobs inherit dryrun=true.
- Job reconciliation performs normal pre-dispatch work (resolve payload, load credentials, select profile, discover targets, build Redfish payload).
- The outbound Redfish command is skipped.
- The job is marked Completed.
- The job status message records the target device URI and the payload that would have been sent.

When dryrun is omitted:
- Behavior defaults to false via Go zero-value semantics for bool fields.
- Existing dispatch behavior is preserved.

## 2. Code Changes

1. apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go
- Added spec field: DryRun bool with JSON key dryrun.
- Added status field: Message string with JSON key message.

2. apis/hardware.fabrica.dev/v1/firmwareupdatecampaign_types.go
- Added spec field: DryRun bool with JSON key dryrun.

3. pkg/reconcilers/firmwareupdatecampaign_reconciler.go
- Updated campaignToChildJob so child FirmwareUpdateJob.Spec.DryRun is copied from FirmwareUpdateCampaign.Spec.DryRun.

4. pkg/reconcilers/firmwareupdatejob_reconciler.go
- Cleared status message at resolve start and on non-dry-run in-progress/error paths.
- Added payload build step before dispatch using new helper:
  - buildRedfishUpdatePayload
- Added dry-run branch in reconcileFirmwareUpdateJob:
  - Skips Redfish POST.
  - Marks job Completed.
  - Clears TaskID and ErrorDetail.
  - Sets status Message with URI and payload.
- Refactored dispatch functions to accept already-built payload:
  - dispatchRedfishWithBackoff now takes actionURI and payload.
  - dispatchRedfishOnce now takes actionURI and payload.
- Added helper functions:
  - compactJSON
  - redfishDispatchURI
  - buildDryRunSuccessMessage

5. pkg/reconcilers/firmwareupdatecampaign_reconciler_test.go
- Added TestCampaignToChildJob_PropagatesDryRun.

6. pkg/reconcilers/firmwareupdatejob_reconciler_test.go
- Added TestRedfishDispatchURI.
- Added TestBuildDryRunSuccessMessageIncludesURIAndPayload.
- Added TestBuildRedfishUpdatePayloadIncludesExpectedFields.

## 3. Validation Performed

Executed:
- go test ./apis/hardware.fabrica.dev/v1 ./pkg/reconcilers

Result:
- API package tests passed.
- Reconciler package run failed in this environment due to unrelated CGO-disabled sqlite test setup requirements:
  - go-sqlite3 requires CGO.

Executed targeted dry-run tests:
- go test ./pkg/reconcilers -run "TestCampaignToChildJob_PropagatesDryRun|TestRedfishDispatchURI|TestBuildDryRunSuccessMessageIncludesURIAndPayload|TestBuildRedfishUpdatePayloadIncludesExpectedFields"

Result:
- All new dry-run focused tests passed.

## 4. Notes

- Dry-run success criteria is implemented as: if request construction/pre-dispatch steps succeed, the job is completed without contacting the device.
- Non-dispatch failures (validation/preparation/payload build failures) still fail the job.
- The status message is intended as the operator-facing dry-run artifact containing both endpoint URI and payload.