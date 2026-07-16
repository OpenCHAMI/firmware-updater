# Error Handling Implementation Handoff

## Summary of Implemented Logic

This change implements the error-handling plan by introducing a dedicated Redfish client package and updating both firmware reconcilers to use it.

Implemented highlights:

- Added a shared Redfish client in pkg/redfish/client.go.
- Centralized Redfish HTTP request construction, TLS settings, endpoint resolution, and JSON response parsing in that package.
- Added structured Redfish error parsing for HTTP 4xx/5xx payloads, including @odata.error and ExtendedInfo message extraction.
- Added a typed Redfish error that carries:
  - StatusCode
  - MessageID
  - Message
  - Resolution
- Updated FirmwareUpdateJob reconciler to use the shared client for:
  - SimpleUpdate dispatch
  - UpdateService action discovery
  - Firmware inventory target discovery
  - generic Redfish GETs
- Updated FirmwareUpdateCampaign reconciler to use the shared client for inventory discovery/member reads.
- Added long-poll strategy for task and inventory verification flows:
  - Up to 30 minutes total polling
  - 15-30 second polling intervals
- Refined task status handling:
  - Warning status is no longer treated as an automatic failure
  - Transitional warning messages (for example reboot-required style states) are treated as in-progress
- Refined inventory health handling:
  - Warning inventory health/conditions no longer cause immediate failure
  - Critical states still fail
- Replaced substring version checks with semantic version comparison using golang.org/x/mod/semver.
- Updated task detail extraction logic to prioritize specific message identifiers and error-like message entries rather than taking the first non-empty field.

## Tests Run

Automated tests run:

- go test ./pkg/reconcilers -count=1
  - Result: PASS

Additional test updates included:

- Updated existing tests to reflect non-fatal warning behavior.
- Added coverage for structured Redfish error surfacing into job error detail.
- Added strict semantic version comparison unit coverage.
- Added fast polling overrides in tests so long-poll logic remains deterministic and does not hang unit runs.

## How To Manually Verify With Server + Curl

The following can be used to manually validate error surfacing and status updates end-to-end.

1. Start the server.

Example (adjust args to your local setup and secrets/runtime config):

go run ./cmd/server serve

2. Submit a FirmwareUpdateCampaign where one BMC target will return a Redfish HTTP 400 with an @odata.error payload.

Example skeleton payload:

{
  "apiVersion": "hardware.fabrica.dev/v1",
  "kind": "FirmwareUpdateCampaign",
  "metadata": {
    "name": "error-surfacing-campaign"
  },
  "spec": {
    "serverProxyAddress": "<server-ip-or-dns>",
    "ociReference": "<oci-ref>",
    "targets": [
      {
        "targetAddress": "<bmc-address>",
        "secretID": "<secret-id>"
      }
    ]
  }
}

Apply with curl against your server API endpoint.

3. Poll jobs and inspect status.errorDetail.

Use the firmwareupdatejobs endpoint and verify that failed jobs include parsed Redfish details from @odata.error ExtendedInfo, including MessageId, Message, and Resolution.

Expected behavior:

- status.jobState transitions to Failed for terminal 4xx Redfish errors.
- status.errorDetail includes structured details instead of only generic HTTP status text.

## Important Usage Notes

- Reconciler code no longer owns low-level Redfish HTTP logic; future Redfish transport/format behavior should be changed in pkg/redfish/client.go.
- Redfish warning health/status states are intentionally non-fatal to avoid false failures during transitional BMC update phases.
- Semantic version checks are strict; substring matching is not used anymore.
- Long-polling behavior is intentionally expanded for firmware operations that can legitimately take many minutes.
- Unit tests use a fast polling override to avoid long waits while preserving production polling behavior.
