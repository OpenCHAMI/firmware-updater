# HANDOFF-LABELS

## 1. Summary of Implemented Logic

Implemented a new dynamic OCI search API endpoint:

- Route: `GET /firmware-search`
- Required query parameter: `registry`
- Additional query parameters are treated as strict annotation-equality filters.
- Search behavior:
  - Uses ORAS SDK to enumerate the registry catalog and repository tags.
  - Fetches each tag manifest and only includes artifacts with:
    - `artifactType == application/vnd.openchami.firmware.bundle.v1+json`
  - Extracts payload digest from the first layer.
  - Applies strict equality against `manifest.annotations` for all provided filters.
- Response (`200`): JSON array of objects with:
  - `reference` (`registry/repository:tag`)
  - `payloadDigest` (first layer digest)
  - `annotations` (full annotation map)

Fault handling implemented per directive:

- Loopback registries (`localhost`, `127.0.0.1`, `::1`) use ORAS `PlainHTTP=true`.
- During scan, item-level `404` conditions are logged and skipped (repository/tag churn does not fail request).
- Registry-wide connectivity failures return HTTP `503`.

## 2. Exact Verified Server Startup Command

```bash
GOTOOLCHAIN=go1.26.3 go run ./cmd/server serve --port 8090 --database-url="file:data.db?cache=shared&_fk=1"
```

## 3. Exact Verified Search Command

```bash
curl -sS "http://127.0.0.1:8090/firmware-search?registry=127.0.0.1:5001&vendor=HPE"
```

This returned only `127.0.0.1:5001/firmware/cray-bmc:1.10.2` and did not include `dell-bios:2.0.0`.

## 4. Detailed Usage Notes

### Endpoint contract

- Request:
  - `GET /firmware-search?registry=<host[:port]>&key1=value1&key2=value2...`
- Filter semantics:
  - All non-`registry` query keys are ANDed strict-equality filters against OCI manifest annotations.
  - If any filter key is missing or value differs, the artifact is excluded.
- Artifact scope:
  - Only OCI manifests with artifact type `application/vnd.openchami.firmware.bundle.v1+json` are eligible.

### Returned fields

Each array item includes:

- `reference`: fully qualified OCI reference with tag
- `payloadDigest`: digest of manifest layer index 0
- `annotations`: complete manifest annotation map

### OpenAPI registration

`/firmware-search` is documented in the custom OpenAPI extension hook and served with the existing OpenAPI docs pipeline.

### Verification commands run

Build prerequisites and compile checks:

```bash
GOTOOLCHAIN=go1.26.3 go mod tidy
GOTOOLCHAIN=go1.26.3 go build ./...
GOTOOLCHAIN=go1.26.3 go test ./...
```

Artifact staging commands used during runtime verification:

```bash
oras push 127.0.0.1:5001/firmware/cray-bmc:1.10.2 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json --annotation "vendor=HPE" --annotation "component=bmc" dummy_firmware.bin:application/vnd.openchami.firmware.payload.v1
oras push 127.0.0.1:5001/firmware/dell-bios:2.0.0 --plain-http --artifact-type application/vnd.openchami.firmware.bundle.v1+json --annotation "vendor=Dell" --annotation "component=bios" dummy_firmware.bin:application/vnd.openchami.firmware.payload.v1
```

### Environment note for the requested `127.0.0.1:5000`

The requested port `5000` was already occupied by a non-registry service in this environment, causing empty/EOF responses for registry API calls. Validation was performed on `127.0.0.1:5001` with a dedicated local registry container. The implementation is port-agnostic and works with `127.0.0.1:5000` when that port is actually serving an OCI registry.

### Files changed

- `pkg/firmwareproxy/resolver.go`
- `cmd/server/openapi_extensions.go`
