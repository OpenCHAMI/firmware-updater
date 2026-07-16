## 1. Scope and Context

The `firmware-updater` repository manages Baseboard Management Controller (BMC) firmware updates via Redfish APIs using a campaign and child-job architecture. Currently, the error handling within the `FirmwareUpdateCampaign` and `FirmwareUpdateJob` reconcilers relies on generalized HTTP status code evaluations and naive string matching.

Specifically, the system treats any HTTP status code between 400 and 499 as an immediate, terminal error without reading the response body. This drops the standard Redfish `MessageRegistry` JSON objects that contain exact failure reasons. Additionally, the system employs a uniform exponential backoff strategy for all Redfish interactions, capped at 4 attempts spanning a maximum of 7 seconds. This is insufficient for long-running BMC flash operations. Finally, state evaluations incorrectly flag "warning" health states as fatal errors, and version verification uses a vulnerable substring match (`strings.Contains`) rather than strict semantic versioning.

The goal of this implementation is to refactor the Redfish HTTP interaction layer out of the main reconcilers into a dedicated, schema-aware client package. This client must parse structured Redfish errors, implement context-aware timeout and polling strategies, handle BMC "warning" states accurately, and enforce strict semantic versioning comparisons.

---

## 2. Code Changes

* **Extraction of Redfish HTTP Client:** Create a dedicated Redfish client package (e.g., `pkg/redfish`) to encapsulate all HTTP client instantiation, request building, and response parsing currently embedded in `firmwareupdatejob_reconciler.go` and `firmwareupdatecampaign_reconciler.go`. The reconcilers should invoke methods on this client rather than executing raw HTTP requests.


* **Structured Error Parsing:** Modify the new client's request execution methods to read the response body for HTTP 4xx and 5xx status codes. Unmarshal the standard Redfish `@odata.error` JSON object to extract the `ExtendedInfo` array. Create a custom typed Go error (e.g., `*RedfishError`) that contains the `MessageId`, `Message`, and `Resolution` strings, and return this to the reconciler to populate the job's `status.errorDetail`.


* **Decoupled Timeout and Backoff Strategies:** Implement multiple retry configurations. Maintain the 4-attempt, 7-second backoff for transient network errors (e.g., connection refused, timeouts). Introduce a distinct, long-running polling configuration specifically for `pollRedfishTaskWithBackoff` and `verifyFirmwareTargetsUpdatedWithBackoff`. This long-running strategy should support up to 30 minutes of total polling with 15-to-30-second intervals.


* **Refined Task Status Logic:** Update the logic in `pollRedfishTaskOnce` and `verifyFirmwareTargetsUpdatedOnce`. Remove the condition that automatically fails a job if `TaskStatus` or `Status.Health` equals "warning". Instead, log the warning or map it to `InProgress` if the associated `MessageId` indicates a normal transitional state (such as a pending reboot).


* **Strict Semantic Versioning:** In `verifyFirmwareTargetsUpdatedOnce`, replace `strings.Contains(installedVersion, resolvedVersion)` with a strict semantic versioning parser (such as `golang.org/x/mod/semver`) to compare the installed firmware version against the resolved target version.


* **Targeted Error Detail Extraction:** Update `redfishTaskDetail` to prioritize specific `MessageId` extraction rather than blindly taking the first non-empty message in the `Messages` array, ensuring informational messages do not mask actual error causes.



---

## 3. Acceptance Criteria

* **AC1 (Architecture):** All raw HTTP client configurations and JSON decoding logic are removed from `firmwareupdatejob_reconciler.go` and `firmwareupdatecampaign_reconciler.go` and encapsulated within a dedicated Redfish client package.


* **AC2 (Error Surfacing):** When a Redfish endpoint returns an HTTP 400-level error containing a `@odata.error` payload, the specific `Message` and `Resolution` text is successfully extracted and surfaced in the `FirmwareUpdateJob` custom resource's `status.errorDetail` field.


* **AC3 (Polling Durations):** Task polling routines execute using a long-polling configuration (up to 30 minutes) rather than the default 7-second transient network backoff.


* **AC4 (Version Comparison):** Version verification utilizes semantic versioning logic, correctly differentiating between versions like `1.2.0` and `1.20.0`, rather than relying on substring matching.


* **AC5 (Testing Requirement):** Testing by actually running the server and executing `curl` commands to confirm functionality is essential. The implementation must be verified by starting the server (`go run ./cmd/server serve ...`), submitting a `FirmwareUpdateCampaign` via `curl`, and demonstrating through `curl` output of the `firmwareupdatejobs/` endpoint that Redfish JSON errors are accurately reflected in `status.errorDetail`.



---

## Output Artifacts

Upon meeting all Acceptance Criteria, generate a handoff file in the planning directory containing:

1. A brief summary of the implemented logic.
2. An explanation of what tests were run and how it could be tested by an individual.
3. Detailed notes on important details for using the code that was implemented, whereby someone with no context could fully utilize the code as expected and fully understand the implementation.