# PHASE 2 HANDOFF - Firmware Management Service

## 1. Implemented Reconciliation Logic Summary

### FirmwareBundle
- Reconciler now performs real OCI discovery using ORAS (oras-go/v2) after pre-flight validation of:
  - spec.registryURL
  - spec.repository
  - spec.tagOrDigest
- For valid specs, it:
  - creates a remote repository client for registryURL/repository
  - resolves the manifest by tag or digest
  - fetches and parses the OCI manifest bytes
  - validates artifact type exactly equals: application/vnd.openchami.firmware.bundle.v1+json
  - extracts manifest annotations into status.extractedMetadata
  - records the first manifest layer digest as status.extractedMetadata.payloadDigest
  - records status.manifestDigest from the resolved manifest descriptor
- Error handling/state transitions:
  - terminal errors (401/403/404, invalid artifact type, other non-transient failures):
    - status.discovered=false
    - status.error set to exact reason
    - reconciliation stops for that run
  - transient errors (503/504, network timeout):
    - retries with exponential backoff from 5s, max 5 attempts
    - status.discovered=false
    - status.error appends transient messages
  - success:
    - status.discovered=true
    - status.error cleared

### FirmwareUpdateJob
- Reconciler preserves idempotency and skips jobs already in:
  - InProgress
  - Completed
  - Failed
- Pre-flight checks validate required spec fields and verify referenced bundle exists.
- Execution flow now:
  - loads referenced FirmwareBundle by spec.bundleName
  - reads payload digest from bundle.status.extractedMetadata.payloadDigest
  - builds proxy URI:
    - http://[serverProxyAddress]:8090/firmware-proxy/layer/[payloadDigest]
  - performs Redfish SimpleUpdate POST to:
    - https://[targetAddress]/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate
  - sends JSON body with ImageURI and Targets
  - uses insecure TLS as required
- Error handling/state transitions:
  - success:
    - status.jobState=InProgress
    - status.taskID extracted from headers/body when available
    - status.errorDetail cleared
  - transient (HTTP 503 or timeout):
    - retries with exponential backoff from 10s, max 3 attempts
    - status.jobState=Validating
    - status.errorDetail appends timeout/service-unavailable details
  - terminal (HTTP 400, unreachable host, and other terminal failures):
    - status.jobState=Failed
    - status.errorDetail records exact reason

## 2. Custom Proxy Endpoint

- Added a live custom HTTP route:
  - GET /firmware-proxy/layer/{digest}
- Implemented in cmd/server/openapi_extensions.go and registered in router startup.
- Behavior:
  - validates digest format (sha256:...)
  - finds discovered FirmwareBundle by payloadDigest metadata
  - resolves and fetches matching layer from OCI registry via ORAS
  - streams bytes directly to HTTP response writer

## 3. Exact Verified Server Startup Command

```bash
GOTOOLCHAIN=go1.26.3 go run ./cmd/server serve --database-url="file:phase2.db?cache=shared&_fk=1"
```

## 4. Exact Verified curl Command (201 Created)

```bash
curl -sS -o /tmp/phase2_create_bundle.json -w "%{http_code}" -X POST http://127.0.0.1:8080/firmwarebundles/ \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"bundle-phase2"},"spec":{"registryURL":"registry.example.org","repository":"firmware/hpe/cray-ex-node-bmc","tagOrDigest":"v2.14.7","credentialsSecret":"reg-creds"}}'
```

- Verified HTTP status code: 201

## 5. Validation Performed

- Dependency added:
  - go get oras.land/oras-go/v2
- Build/tooling checks run:
  - go mod tidy
  - go build ./...
  - go test ./...
- Result:
  - build passed
  - tests passed, including reconciler state transition/error handling tests

## 6. Service Usage Notes (For Someone With No Prior Context)

### API Surface
- FirmwareBundle resource endpoints:
  - POST /firmwarebundles/
  - GET /firmwarebundles/
  - GET /firmwarebundles/{uid}/
  - PUT/PATCH/DELETE /firmwarebundles/{uid}/
  - PUT/PATCH /firmwarebundles/{uid}/status/
- FirmwareUpdateJob resource endpoints:
  - POST /firmwareupdatejobs/
  - GET /firmwareupdatejobs/
  - GET /firmwareupdatejobs/{uid}/
  - PUT/PATCH/DELETE /firmwareupdatejobs/{uid}/
  - PUT/PATCH /firmwareupdatejobs/{uid}/status/
- Custom proxy endpoint:
  - GET /firmware-proxy/layer/{digest}

### FirmwareBundle Requirements
- spec.registryURL: registry host only (no scheme, no path)
- spec.repository: valid OCI repository path
- spec.tagOrDigest: valid tag or sha256 digest
- spec.credentialsSecret: optional

### FirmwareUpdateJob Requirements
- spec.targetAddress, spec.username, spec.password
- spec.bundleName must match existing FirmwareBundle metadata.name
- spec.targets must be non-empty Redfish target list
- spec.serverProxyAddress must point to this service host used by BMC

### Phase 2 Operational Expectations
- FirmwareBundle discovery now requires reachable OCI registry and correct artifact type.
- For unreachable/nonexistent registries, bundle status will contain explicit terminal errors.
- FirmwareUpdateJob requires payloadDigest in associated bundle status metadata.
- Job submission sends Redfish SimpleUpdate and transitions to InProgress only when accepted.
- Re-running reconciler is idempotent for terminal/active job states and safe for bundle rediscovery.

### Important Network/TLS Notes
- Redfish requests are sent over HTTPS with insecure TLS verification enabled.
- BMC must be able to reach the service at:
  - http://[serverProxyAddress]:8090/firmware-proxy/layer/[payloadDigest]
  (as provided in FirmwareUpdateJob spec)

### OpenAPI/Docs
- The proxy endpoint is registered in OpenAPI via custom path extension.
- OpenAPI and docs endpoints:
  - GET /openapi.json
  - GET /docs
