# HANDOFF-PHASE2

## 1) Summary of Implemented Logic

Implemented FirmwareUpdateJob version-discovery mode with OCI manifest annotation filtering and semantic-version resolution.

### What changed

- API types updated:
  - Added `DiscoverySpec` with required fields:
    - `repository`
    - `hardwareModel`
    - `version`
  - `spec.ociReference` changed from required string to optional pointer.
  - Added `spec.discovery` optional pointer.
  - Added status fields:
    - `status.resolvedVersion`
    - `status.resolvedDigest`
- Validation updated (`FirmwareUpdateJob.Validate`):
  - Enforces exactly one of `spec.ociReference` or `spec.discovery`.
  - Rejects both present and both absent.
  - Validates `spec.discovery.*` required values when discovery mode is used.
- Discovery resolver implemented (`pkg/firmwareproxy/resolver.go`):
  - Lists all tags from `spec.discovery.repository` via ORAS.
  - Fetches each tag manifest.
  - Filters manifests by:
    - `artifactType == application/vnd.openchami.firmware.bundle.v1+json`
    - `dev.fabrica.hardware.compatible` contains requested hardware model
    - valid `org.opencontainers.image.version` SemVer annotation
  - Selects resolved candidate by:
    - highest SemVer when `version=latest`
    - exact SemVer match otherwise
  - Extracts payload layer digest and returns resolved metadata.
- Reconciler integration (`pkg/reconcilers/firmwareupdatejob_reconciler.go`):
  - Resolves from explicit OCI reference OR discovery mode.
  - Persists `status.resolvedDigest` and `status.resolvedVersion` during reconciliation.
  - Preserves existing retry/backoff and terminal error behavior.
- Tests added (`pkg/firmwareproxy/resolver_test.go`):
  - Highest SemVer selection for `latest`.
  - Exact version selection.
  - Invalid target version handling.
  - Hardware compatibility parsing.
- Regeneration + verification completed:
  - `fabrica generate`
  - `go mod tidy`
  - `go build ./...`
  - `go test ./...`

## 2) Exact Verified Command

The following command was executed successfully and returned `HTTP/1.1 201 Created`:

```bash
curl -sS -i -X POST http://127.0.0.1:18080/firmwareupdatejobs/ \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"phase2-discovery-demo"},"spec":{"targetAddress":"192.0.2.10","username":"admin","password":"admin","serverProxyAddress":"127.0.0.1","component":"BMC","discovery":{"repository":"127.0.0.1:5000/firmware/test-bmc","hardwareModel":"x1000","version":"latest"}}}'
```

Also verified (both returned `400` as expected):

- both set (`spec.ociReference` + `spec.discovery`)
- neither set

## 3) Important Usage Notes

- Discovery mode input contract:
  - Provide exactly one source:
    - `spec.ociReference`, or
    - `spec.discovery`
- Discovery annotations expected on candidate manifests:
  - `dev.fabrica.hardware.compatible`: list/string containing hardware model token
  - `org.opencontainers.image.version`: valid SemVer value (for example `1.2.3` or `v1.2.3`)
- Version targeting behavior:
  - `version: latest` chooses highest compatible SemVer.
  - Any other `version` is treated as an exact SemVer target.
- Payload selection behavior:
  - Uses first OCI layer digest from selected manifest as payload digest.
  - Stores digest in in-memory payload index for proxy streaming.
- Status behavior:
  - `status.resolvedDigest` set after payload resolution.
  - `status.resolvedVersion` set for discovery-resolved jobs.
- Local dev startup:
  - Ensure sqlite DSN includes `_fk=1` (for example `file:./phase2_test.db?cache=shared&_fk=1`).
- Local registry note:
  - Loopback registries (`localhost`, `127.0.0.1`, `::1`) are handled with `PlainHTTP` in ORAS client configuration.
