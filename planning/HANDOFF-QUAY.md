# Quay Credential Support Handoff

## Summary
Implemented scoped registry authentication for firmware OCI resolution by refactoring `pkg/firmwareproxy` to a stateful `Resolver` type. The resolver now configures ORAS `auth.Client` and only returns credentials when the outbound request registry exactly matches the configured registry domain.

Server configuration now includes:
- `FIRMWARE_UPDATER_REGISTRY_DOMAIN`
- `FIRMWARE_UPDATER_REGISTRY_USERNAME`
- `FIRMWARE_UPDATER_REGISTRY_PASSWORD`

The firmware-proxy HTTP route and the FirmwareUpdateJob reconciler both use resolver instances configured from server `Config`, so runtime resolution paths use the same credential policy.

## Verified curl command
This command was run successfully against the updated service:

```bash
curl -i --fail http://127.0.0.1:18080/health
```

Observed response:
- HTTP `200 OK`
- Body: `{"status":"healthy","service":"firmware-updater"}`

## Implementation details and usage notes
1. Resolver refactor:
- Previous package-level functions were converted to methods:
  - `(*Resolver).ResolvePayload`
  - `(*Resolver).ResolvePayloadFromDiscovery`
  - `(*Resolver).StreamPayloadLayer`
- New constructor: `firmwareproxy.NewResolver(domain, username, password)`.

2. Credential scoping behavior:
- Auth is attached via `repo.Client = &auth.Client{Credential: ...}` for each repository client.
- Credential callback normalizes and compares target registry host to configured domain.
- If no exact host match, empty credentials are returned and nothing is leaked.

3. Environment-variable mapping:
- Viper env prefix was updated to `FIRMWARE_UPDATER`.
- `viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))` was added.
- This ensures underscore-based env vars map correctly to config keys.

4. Special-character credentials (`$` prefix):
- The service accepts credential strings with leading `$` when provided as quoted env values, for example:

```bash
FIRMWARE_UPDATER_REGISTRY_USERNAME='$robot'
FIRMWARE_UPDATER_REGISTRY_PASSWORD='$token'
```

5. Reconciler integration:
- Added `reconcilers.SetFirmwareProxyResolver(*firmwareproxy.Resolver)`.
- Server startup now injects a resolver into reconciler package before controller start.
- Reconciler backoff helpers now call resolver methods instead of removed package functions.

6. Route integration:
- `registerFirmwareProxyRoute` now accepts a resolver parameter.
- Route streams payload layers via resolver method calls.

7. Tests and validation performed:
- Added resolver auth tests proving credential behavior:
  - credential returned for matching host
  - empty credential returned for non-matching host
- Executed:

```bash
GOTOOLCHAIN=go1.26.3 go test ./pkg/firmwareproxy ./pkg/reconcilers
GOTOOLCHAIN=go1.26.3 go build ./...
```

## Notes for private Quay validation
To validate private Quay pull behavior end-to-end, run the server with:

```bash
FIRMWARE_UPDATER_REGISTRY_DOMAIN='quay.io'
FIRMWARE_UPDATER_REGISTRY_USERNAME='<quay-robot-or-user>'
FIRMWARE_UPDATER_REGISTRY_PASSWORD='<quay-token-or-password>'
```

Then submit or reconcile a FirmwareUpdateJob using an OCI reference under that same registry host. If the reference host differs (for example `registry-1.docker.io`), credentials will not be sent by design.
