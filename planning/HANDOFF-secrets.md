# Secrets Integration Handoff

## 1. Implementation Summary

This implementation separates credential creation from runtime credential consumption:

- Out-of-band writer: a new CLI at `cmd/secret-cli/main.go` accepts plaintext `--username` and `--password`, serializes them as `{"username":"...","password":"..."}`, and stores encrypted payloads by ID using `StoreSecretByID` in the Magellan secrets library.
- Read-only service consumer: the server initializes one global `SecretStore` instance at startup and exposes no HTTP endpoint that accepts plaintext credentials.
- Runtime usage: `FirmwareUpdateJob.spec` now uses `secretID` instead of `username`/`password`. The reconciler decrypts credentials via `GetSecretByID(secretID)` and applies them to Redfish requests at execution time.

To keep behavior stable with current `github.com/OpenCHAMI/magellan v0.5.1` file-handle bugs, fallback logic was added in both the server and secret CLI while still using the Magellan secrets types and methods.

## 2. Encrypted Store Path and Application State

- Encrypted store path: configurable by `--secrets-file` (default `secrets.json`).
- Verified runtime path in manual test: `/tmp/fw-secrets.json`.
- Application state management:
  - Server initializes the store once in `cmd/server/main.go` (`initializeSecretStore`).
  - Store is attached to process-wide state in `internal/secretsruntime/store.go` via `SetStore(...)`.
  - Reconciler reads it via `secretsruntime.GetStore()`.

## 3. Exact Verified curl Command

This command successfully created a `FirmwareUpdateJob` using the new `secretID` model against the running server:

```bash
curl -sS -X POST http://127.0.0.1:18090/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"secretid-demo"},"spec":{"targetAddress":"192.0.2.10","secretID":"demo-bmc","serverProxyAddress":"127.0.0.1","ociReference":"127.0.0.1:5000/firmware/test-bmc:1.0.0","targets":["/redfish/v1/UpdateService/FirmwareInventory/BMC"]}}'
```

Observed response included `201` creation payload with:

- `"spec":{"secretID":"demo-bmc", ...}`
- generated UID `firmwareupdatejob-38279b12`

## 4. Usage Notes

### 4.1 Generate and export a valid MASTER_KEY

Generate a valid 64-character hex AES-256 key:

```bash
export MASTER_KEY="$(openssl rand -hex 32)"
```

Validation now enforces this format at server startup. Verified failure behavior:

```text
Error: MASTER_KEY must be a 64-character hex string
```

### 4.2 Generate encrypted secrets.json out-of-band

Use the new CLI:

```bash
go run ./cmd/secret-cli \
  --secret-id demo-bmc \
  --username admin \
  --password password \
  --store-path ./secrets.json
```

This writes encrypted values keyed by `secret-id`.

### 4.3 Provision/mount secrets.json into service runtime

Ensure the exact secrets file is available to the server container/process and pass its location with `--secrets-file` (or default to `./secrets.json`):

```bash
MASTER_KEY="$MASTER_KEY" ./tmp/server serve --secrets-file ./secrets.json
```

For containers/Kubernetes, mount the encrypted file into the container filesystem (for example `/etc/firmware-updater/secrets.json`) and run:

```bash
MASTER_KEY="$MASTER_KEY" ./server serve --secrets-file /etc/firmware-updater/secrets.json
```

The server only reads from this store at runtime; it does not expose credential-ingest HTTP APIs.
