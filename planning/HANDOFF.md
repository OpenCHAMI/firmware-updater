# HANDOFF

## 1. Reconciliation Logic Summary

`FirmwareUpdateJob` reconciliation is implemented in [pkg/reconcilers/firmwareupdatejob_reconciler.go](pkg/reconcilers/firmwareupdatejob_reconciler.go) with the following behavior:

- Idempotency guard:
  - Reconciliation exits immediately when `status.jobState` is `InProgress`, `Completed`, or `Failed`.
- State flow:
  - Default empty status is treated as `Pending`.
  - Job is moved to `Resolving` before OCI operations.
- JIT OCI resolution:
  - Parses `spec.ociReference`.
  - Uses ORAS to fetch the OCI manifest.
  - Verifies artifact type equals `application/vnd.openchami.firmware.bundle.v1+json`.
  - Extracts the first layer digest as payload digest.
- Proxy dispatch:
  - Builds `ImageURI` as `http://<serverProxyAddress>:8090/firmware-proxy/layer/<payloadDigest>`.
  - Calls Redfish `SimpleUpdate` at `https://<targetAddress>/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`.
  - Uses insecure TLS (`InsecureSkipVerify`) as required.
  - Sends JSON body with `ImageURI` and `Targets`.
- Error model:
  - HTTP 4xx-style errors are treated as terminal and set `jobState=Failed` with `errorDetail`.
  - Timeouts/503-like failures are treated as transient with exponential backoff inside the reconcile cycle.
  - A bounded transient retry budget is enforced; when exhausted, the job is marked `Failed` with timeout/connectivity details.
- Success path:
  - Sets `jobState=InProgress` and stores Redfish task identifier when available.

## 2. Proxy Endpoint Details

Proxy implementation is in [cmd/server/openapi_extensions.go](cmd/server/openapi_extensions.go) and shared ORAS resolver utilities are in [pkg/firmwareproxy/resolver.go](pkg/firmwareproxy/resolver.go).

- Route: `GET /firmware-proxy/layer/{digest}`
- Behavior:
  - Validates digest format.
  - Looks up digest-to-repository mapping captured during JIT resolve.
  - Pulls payload layer bytes from OCI with ORAS.
  - Streams bytes as `application/octet-stream`.
- Localhost support:
  - ORAS uses `PlainHTTP=true` for loopback registries (`localhost`, `127.0.0.1`, `::1`).

## 3. Exact Verified Create Command

This command was run successfully against the local server and returned `201 Created` with UID `firmwareupdatejob-cff75487`:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "jit-test-job-3"},
    "spec": {
      "targetAddress": "192.0.2.123",
      "username": "admin",
      "password": "password",
      "ociReference": "localhost:5000/firmware/test-bmc:1.0.0",
      "targets": ["/redfish/v1/UpdateService/FirmwareInventory/BMC"],
      "serverProxyAddress": "127.0.0.1"
    }
  }'
```

## 4. How To Run The Service End-to-End

### 4.1 Scaffolded project location

- Service code lives in the repository root.
- The project was scaffolded with Fabrica (not hand-generated):
  - `fabrica init firmware-updater --group hardware.fabrica.dev --storage-type ent --db sqlite --reconcile --events`
  - `fabrica add resource FirmwareUpdateJob`
  - `fabrica generate`

### 4.2 Build and run

```bash
GOTOOLCHAIN=go1.26.3 go mod tidy
GOTOOLCHAIN=go1.26.3 go build ./...
GOTOOLCHAIN=go1.26.3 go run ./cmd/server serve --port 8090 --database-url="file:data.db?cache=shared&_fk=1"
```

### 4.3 OCI artifact prep

Preferred (as specified):

```bash
docker run -d -p 5000:5000 --name local-oci-registry registry:2
echo "dummy payload" > dummy.bin
oras push localhost:5000/firmware/test-bmc:1.0.0 \
  --artifact-type application/vnd.openchami.firmware.bundle.v1+json \
  dummy.bin:application/vnd.openchami.firmware.payload.v1
```

If Docker daemon is unavailable, a compatible fallback used during verification is a standalone distribution registry process bound to `:5000`.

### 4.4 Observe job status

```bash
curl -sS http://127.0.0.1:8090/firmwareupdatejobs/<uid>/
```

Expected progression for unreachable dummy BMC target:

- `Pending` (implicit/default)
- `Resolving`
- `Failed` with timeout/connectivity information in `status.errorDetail`

Observed validation result:

- `firmwareupdatejob-cff75487` reached `status.jobState="Failed"`
- `status.errorDetail` contained a Redfish connection timeout:
  - `context deadline exceeded (Client.Timeout exceeded while awaiting headers)`

Additional proxy verification:

- `GET /firmware-proxy/layer/sha256:3b1da25e4f135edf46ed0ec00072ab739cbcf26d54922e1957ee14209f179aec` returned `dummy payload` bytes.

This demonstrates:

- OCI manifest + payload digest resolution succeeded.
- Redfish dispatch was attempted and failed due to target connectivity, not OCI lookup failure.

## 5. API Contract Implemented

`FirmwareUpdateJob.spec` required fields:

- `targetAddress` (string)
- `username` (string)
- `password` (string)
- `ociReference` (string)
- `targets` (array of strings, min 1)
- `serverProxyAddress` (string)

`FirmwareUpdateJob.status` fields:

- `jobState` (Pending, Resolving, InProgress, Failed, Completed)
- `taskID` (optional)
- `errorDetail` (optional)

## 6. Key Files Changed

- [apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go](apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go)
- [pkg/firmwareproxy/resolver.go](pkg/firmwareproxy/resolver.go)
- [cmd/server/openapi_extensions.go](cmd/server/openapi_extensions.go)
- [cmd/server/main.go](cmd/server/main.go)
- [pkg/reconcilers/firmwareupdatejob_reconciler.go](pkg/reconcilers/firmwareupdatejob_reconciler.go)
