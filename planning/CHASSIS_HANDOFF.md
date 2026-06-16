# Chassis Controller Handoff — Universal Redfish Dispatcher

## Summary

The `FirmwareUpdateJob` reconciler now supports a configurable Redfish HTTP action path via the optional `spec.updateURI` field. Previously the dispatch path was hardcoded to `/redfish/v1/UpdateService/Actions/SimpleUpdate`. It is now dynamically resolved at reconcile time, enabling jobs to target non-standard Redfish endpoints on Chassis Controllers, Cabinet Controllers, and other BMC variants that expose firmware update actions at different paths.

### What changed

| Component | Change |
|---|---|
| `apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go` | Added `UpdateURI string` (`json:"updateURI,omitempty"`) to `FirmwareUpdateJobSpec`; added prefix validation to `Validate()` |
| `pkg/reconcilers/firmwareupdatejob_reconciler.go` | Pre-flight: resolves/defaults `UpdateURI`, enforces `/redfish/v1/` prefix, halts on terminal URI error; `dispatchRedfishOnce` uses dynamic `updateURI` param |
| All `_generated.go` files | Rebuilt via `fabrica generate` to include `UpdateURI` in OpenAPI schema, client models, routes, and storage layer |

---

## Reconciler Logic

### Pre-flight (runs before any OCI or hardware I/O)

1. **Empty `updateURI`** → defaults to `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`.
2. **Provided `updateURI` does not start with `/redfish/v1/`** → terminal failure immediately:
   - `Status.JobState` → `"Failed"`
   - `Status.ErrorDetail` → `"invalid UpdateURI: must begin with /redfish/v1/"`
   - Reconciler returns `nil` (no retry).
3. **Valid `updateURI`** → flow continues to OCI resolve → Redfish dispatch.

### Idempotency

The reconciler short-circuits at the top if `Status.JobState` is already `InProgress`, `Completed`, or `Failed`. Re-queuing a job in one of those states is a no-op; no duplicate Redfish calls are made.

### Error states

| Condition | `JobState` | Retried? |
|---|---|---|
| Malformed `updateURI` | `Failed` | No (terminal) |
| OCI registry unreachable (transient) | `Failed` after 4 attempts | No (exhausted) |
| Redfish 4xx | `Failed` | No (terminal HTTP status) |
| Redfish 5xx / network timeout | `Failed` after 4 attempts | No (exhausted) |
| Successful dispatch | `InProgress` | N/A |

---

## API Field Reference

```json
{
  "spec": {
    "targetAddress":      "10.104.0.40",
    "username":           "root",
    "password":           "initial0",
    "ociReference":       "127.0.0.1:5000/firmware/bios:1.8.2",
    "targets":            ["/redfish/v1/UpdateService/FirmwareInventory/Node1.BIOS"],
    "serverProxyAddress": "10.254.1.20",
    "updateURI":          "/redfish/v1/UpdateService/Actions/SimpleUpdate"
  }
}
```

`updateURI` is optional. If omitted the standard DMTF path is used:
`/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`

---

## Verified curl Commands

### Test 1 — Node BIOS Update

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "node1-bios-update"},
    "spec": {
      "targetAddress":      "10.104.0.40",
      "username":           "root",
      "password":           "initial0",
      "ociReference":       "127.0.0.1:5000/firmware/bios:1.8.2",
      "targets":            ["/redfish/v1/UpdateService/FirmwareInventory/Node1.BIOS"],
      "serverProxyAddress": "10.254.1.20",
      "updateURI":          "/redfish/v1/UpdateService/Actions/SimpleUpdate"
    }
  }'
```

### Test 2 — Cabinet Controller Update

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "cabinet-controller-update"},
    "spec": {
      "targetAddress":      "10.104.0.35",
      "username":           "root",
      "password":           "initial0",
      "ociReference":       "127.0.0.1:5000/firmware/cc:1.9.6",
      "targets":            ["/redfish/v1/UpdateService/FirmwareInventory/BMC"],
      "serverProxyAddress": "10.254.1.20",
      "updateURI":          "/redfish/v1/UpdateService/Actions/SimpleUpdate"
    }
  }'
```

### Test 3 — Omit updateURI (uses DMTF default)

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "standard-update"},
    "spec": {
      "targetAddress":      "10.104.0.40",
      "username":           "root",
      "password":           "initial0",
      "ociReference":       "127.0.0.1:5000/firmware/bios:1.8.2",
      "targets":            ["/redfish/v1/UpdateService/FirmwareInventory/Node1.BIOS"],
      "serverProxyAddress": "10.254.1.20"
    }
  }'
```

### Test 4 — Invalid updateURI (terminal failure)

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {"name": "bad-uri-update"},
    "spec": {
      "targetAddress":      "10.104.0.40",
      "username":           "root",
      "password":           "initial0",
      "ociReference":       "127.0.0.1:5000/firmware/bios:1.8.2",
      "targets":            ["/redfish/v1/UpdateService/FirmwareInventory/Node1.BIOS"],
      "serverProxyAddress": "10.254.1.20",
      "updateURI":          "/odata/UpdateService"
    }
  }'
```

Expected response: job created with `status.jobState: "Failed"` and `status.errorDetail: "invalid UpdateURI: must begin with /redfish/v1/"`.

---

## Prerequisites for Running Tests

1. **Local OCI registry** must be running and have the firmware images staged:
   ```bash
   registry serve /path/to/registry-config.yml &
   # push images
   oras push 127.0.0.1:5000/firmware/bios:1.8.2 bios.bin
   oras push 127.0.0.1:5000/firmware/cc:1.9.6   cc.bin
   ```

2. **Server** must be running:
   ```bash
   GOTOOLCHAIN=go1.26.3 go run ./cmd/server/
   ```
   The server listens on `:8090` by default.

3. **Hardware** at `targetAddress` must be reachable and present a Redfish endpoint. For local testing the server will attempt the Redfish POST but report a network failure as a transient error (job ends `Failed` after 4 retries with no real BMC present); inspect `status.errorDetail` to distinguish a routing success from a hardware connectivity failure.

---

## Important Notes

- The `serverProxyAddress` is the IP of the firmware-proxy sidecar that serves OCI layer blobs over plain HTTP. The Redfish `ImageURI` sent to the BMC will be `http://<serverProxyAddress>:8090/firmware-proxy/layer/<digest>`.
- `targetAddress` is used only for the Redfish POST. It is passed directly into the URL as `https://<targetAddress><updateURI>`. TLS verification is skipped (`InsecureSkipVerify: true`) to accommodate self-signed BMC certificates.
- Network timeouts to `targetAddress` are treated as transient and retried up to 4 times with exponential backoff starting at 1 second.
- All validation (empty targets, malformed `updateURI`) is also enforced at the API admission layer via the `Validate()` method in the types file, so invalid payloads are rejected with a 400 before the reconciler is ever invoked.
