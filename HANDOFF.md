# PHASE 1 HANDOFF - Firmware Management Service

## 1. Implemented Reconciliation Logic Summary

### FirmwareBundle
- Reconciler validates `spec.registryURL`, `spec.repository`, and `spec.tagOrDigest` format.
- On valid input, it performs the Phase 1 mock discovery flow by:
  - setting `status.discovered=true`
  - setting a deterministic mock `status.manifestDigest` using SHA-256 of `registry/repository@reference`
  - setting `status.extractedMetadata` with test key/value metadata
  - clearing `status.error`
- On invalid input, it sets:
  - `status.discovered=false`
  - `status.error` with a validation message
- No OCI registry network calls are performed in this phase.

### FirmwareUpdateJob
- Reconciler enforces idempotency for terminal/active states: `InProgress`, `Completed`, `Failed` are skipped.
- It validates required fields for targets and credentials:
  - `spec.targetAddress`, `spec.username`, `spec.password`, `spec.bundleName`, `spec.serverProxyAddress`, and non-empty `spec.targets`.
- It verifies `spec.bundleName` references an existing `FirmwareBundle` by listing bundles via the generated reconciliation client.
- For valid jobs in `Pending` (or empty state), it performs the Phase 1 mock state transition to `Validating`.
- On validation failure, it sets:
  - `status.jobState=Failed`
  - `status.errorDetail` with the exact reason
- No Redfish network calls are performed in this phase.

## 2. Exact Verified Server Startup Command

```bash
GOTOOLCHAIN=go1.26.3 go run ./cmd/server serve --database-url="file:data.db?cache=shared&_fk=1"
```

## 3. Exact Verified curl Command (201 Created)

```bash
curl -sS -o /tmp/fms_create_response.json -w "%{http_code}" -X POST http://127.0.0.1:8080/firmwarebundles/ \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"bundle-1"},"spec":{"registryURL":"registry.example.org","repository":"firmware/hpe/cray-ex-node-bmc","tagOrDigest":"v2.14.7","credentialsSecret":"reg-creds"}}'
```

- Verified HTTP status: `201`

## 4. Usage Notes (No Prior Context Required)

### Framework and Project Facts
- Service is scaffolded with Fabrica using:
  - project/module name: `firmware-manager`
  - API group: `hardware.fabrica.dev`
  - storage backend: `ent`
  - database driver: `sqlite`
  - enabled features: reconciliation, events, storage
- API version currently implemented: `v1`.

### Resource Endpoints
- FirmwareBundle:
  - `POST /firmwarebundles/`
  - `GET /firmwarebundles/`
  - `GET /firmwarebundles/{uid}/`
  - `PUT|PATCH|DELETE /firmwarebundles/{uid}/`
  - `PUT|PATCH /firmwarebundles/{uid}/status/`
- FirmwareUpdateJob:
  - `POST /firmwareupdatejobs/`
  - `GET /firmwareupdatejobs/`
  - `GET /firmwareupdatejobs/{uid}/`
  - `PUT|PATCH|DELETE /firmwareupdatejobs/{uid}/`
  - `PUT|PATCH /firmwareupdatejobs/{uid}/status/`

### Required Create Payloads
- FirmwareBundle `spec` requires:
  - `registryURL` (string)
  - `repository` (string)
  - `tagOrDigest` (string)
  - `credentialsSecret` (optional string)
- FirmwareUpdateJob `spec` requires:
  - `targetAddress` (string)
  - `username` (string)
  - `password` (string)
  - `bundleName` (string)
  - `targets` (non-empty string array)
  - `serverProxyAddress` (string)

### Reconciliation Behavior to Expect
- Creating a valid FirmwareBundle returns immediately with initial status and then reconciliation updates status to discovered metadata.
- Creating a valid FirmwareUpdateJob requires that `bundleName` match an existing FirmwareBundle `metadata.name`; then job state progresses to `Validating` in this phase.

### Validation and Error Semantics
- Invalid FirmwareBundle format values are reported in `status.error`.
- Invalid FirmwareUpdateJob values or missing bundle references are reported in `status.errorDetail` and state is set to `Failed`.

### Verification Performed
- `go mod tidy` executed.
- `go build ./...` executed successfully.
- `go test ./...` executed successfully (includes table-driven tests for custom reconciliation logic).
- Server started locally and bound to port 8080.
- POST create API call succeeded with HTTP 201.
