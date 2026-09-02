# Handoff: OCI Firmware Image API

Status: **Implemented**
Plan: [OCI_API.md](OCI_API.md)

## Summary

Implemented an OCI-registry-backed firmware image API. It stores firmware payloads as OCI image manifests with artifact type `application/vnd.openchami.firmware.bundle.v1+json`, exposes stored firmware metadata in Fabrica `apiVersion`/`kind`/`metadata`/`spec`/`status` resources, searches metadata, and deletes a selected manifest.

The feature is deliberately registry-backed: it does not add an Ent schema, database migration, Fabrica resource, or reconciler. Firmware images remain compatible with the existing `firmwareproxy` resolver because uploads use an OCI image manifest with the expected artifact type, version annotation, compatible-hardware annotation, and layer title annotation.

## HTTP API

All routes are registered under `/firmwareimages`.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/firmwareimages/` | Upload and tag a firmware image. |
| `GET` | `/firmwareimages/?repository=...` | List firmware images in one repository; omit `repository` to list all repositories under the configured registry host. |
| `GET` | `/firmwareimages/search?repository=...&manufacturer=...&latest=true` | Return every matching image, or only the newest semantic version when `latest=true`. |
| `GET` | `/firmwareimages/image?repository=...&tag=...` | Retrieve one firmware image's metadata. |
| `DELETE` | `/firmwareimages/?repository=...&tag=...&force=false` | Remove the selected firmware manifest. |

### Upload request

`POST /firmwareimages/` accepts `multipart/form-data` with:

- `metadata`: JSON matching `firmwareimages.PushRequest`.
- `file`: the firmware binary.

Required metadata fields are `repository`, `tag`, `version`, and at least one `targets` entry. Optional `models`, `softwareIds`, and `versionString` values are stored in the OCI manifest and returned by image endpoints. The upload size is limited to 2 GiB by the HTTP handler.

Example:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareimages/ \
  -F 'metadata={"repository":"127.0.0.1:5000/firmware/ilo","tag":"3.0.1","version":"3.0.1","targets":["iLO 5"],"models":["ProLiant DL325 Gen10"],"softwareIds":["ilo5"],"versionString":"3.0.1 (vendor)","manufacturer":"hpe","deviceType":"nodeBMC","tags":["production"]};type=application/json' \
  -F 'file=@./dummy-firmware.bin;type=application/octet-stream'
```

On success it returns `201 Created` and a `FirmwareImageMetadata` object containing the repository, tag, manifest digest, version, payload filename, targets, models, software IDs, version string, optional metadata, and payload size.

### Search behavior

Supported filters are `manufacturer`, `deviceType`, `model`, `target`, `softwareId`, `version`, `versionString`, `tag`, and `filename`.

All supplied filters must match. Manufacturer, device type, and version use case-insensitive exact matching; model, target, software ID, version string, free-form tag, and filename use case-insensitive substring matching. At least one supported filter is required unless `latest=true` is supplied. `latest=true` returns only the matching image with the highest valid semantic `version`; invalid versions are excluded and tag order breaks equal-version ties. The response uses this envelope:

Example, retrieving the latest image that matches a Slingshot software ID in one repository:

```bash
curl -sS "http://localhost:8080/firmwareimages/search?repository=registry:5000/firmware&softwareId=sc:*:*:*&latest=true"
```

```json
{
  "items": [],
  "count": 0
}
```

### Deletion behavior

OCI registries delete manifests by digest. Before deleting, the API resolves all repository tags and detects whether another tag points to the same manifest digest.

- A unique manifest is deleted and returns `204 No Content`.
- A shared manifest returns `409 Conflict` unless `force=true` is supplied.
- `force=true` deletes the shared manifest, removing every tag that resolves to that digest.
- OCI blob garbage collection is managed by the registry and is not triggered by this service.

## Metadata Stored in OCI

The payload layer is annotated with `org.opencontainers.image.title` using the submitted filename. The manifest uses these annotations:

| Annotation | Source |
|---|---|
| `org.opencontainers.image.version` | `metadata.version` |
| `dev.fabrica.hardware.compatible` | `metadata.targets` (comma-separated; retained for resolver compatibility) |
| `dev.fabrica.hardware.manufacturer` | `metadata.manufacturer` |
| `dev.fabrica.hardware.deviceType` | `metadata.deviceType` |
| `dev.fabrica.firmware.tags` | `metadata.tags` (comma-separated) |
| `dev.fabrica.firmware.models` | `metadata.models` (comma-separated) |
| `dev.fabrica.firmware.softwareIds` | `metadata.softwareIds` (comma-separated) |
| `dev.fabrica.firmware.versionString` | `metadata.versionString` |

## Files Changed

- **New:** [pkg/firmwareimages/types.go](../pkg/firmwareimages/types.go) — API request/response types and OCI annotation constants.
- **New:** [pkg/firmwareimages/store.go](../pkg/firmwareimages/store.go) — validation, OCI payload push, image-manifest packing, and manifest tagging.
- **New:** [pkg/firmwareimages/catalog.go](../pkg/firmwareimages/catalog.go) — repository enumeration, list/get/search logic, shared-tag-safe deletion, registry error mapping, and `force` parsing.
- **New:** [pkg/firmwareimages/types_test.go](../pkg/firmwareimages/types_test.go) — unit tests for filter semantics, metadata annotation normalization, and `force` validation.
- **New:** [cmd/server/firmwareimage_routes.go](../cmd/server/firmwareimage_routes.go) — chi route registration, multipart parsing, request handling, and HTTP error responses.
- **Modified:** [pkg/firmwareproxy/resolver.go](../pkg/firmwareproxy/resolver.go) — exported authenticated `NewRepository` and `NewRegistry` helpers so all OCI interactions use existing credentials and HTTPS by default; the insecure override only bypasses certificate verification.
- **Modified:** [cmd/server/main.go](../cmd/server/main.go) — registers the new routes and adds `registry_host` configuration, available as `--registry-host` or `FIRMWARE_UPDATER_REGISTRY_HOST`.
- **Modified:** [docker-compose.yml](../docker-compose.yml) — configures the updater to access the Compose registry as `registry:5000` with self-signed-certificate verification disabled.
- **New:** [tools/upload_images_to_registry.py](../tools/upload_images_to_registry.py) — imports `images.json` through the firmware image API, creates dummy payloads when staged files are absent, supports `--dry-run`, and validates that `--repository` includes both registry host and repository path.
- **Modified:** [OCI_API.md](OCI_API.md) — records the final query endpoint shape, metadata fields, search filters, latest-version behavior, transport behavior, Compose configuration, and bulk importer.

## Error Handling

- Validation errors return `400 Bad Request`.
- Missing firmware tags or non-firmware manifests return `404 Not Found`.
- A shared manifest without `force=true` returns `409 Conflict`.
- OCI registry connectivity and unexpected registry errors map to `503 Service Unavailable`.
- OCI registry authentication and invalid-registry-request responses map to `400 Bad Request` under the existing service error convention.

## Verification Performed

- `gofmt -w pkg/firmwareimages/store.go`
- `go test ./pkg/firmwareimages ./cmd/server`
- `go build ./cmd/server`
- Go diagnostics were clean for the new package, server route file, server wiring, resolver changes, and plan.

## Manual Validation

Start the registry and service with an all-repositories registry host configured:

```bash
go run ./cmd/server serve --port 8090 --registry-host 127.0.0.1:5000
```

Then upload an image using the request above and verify it:

```bash
curl -sS 'http://127.0.0.1:8090/firmwareimages/?repository=127.0.0.1:5000/firmware/ilo' | jq
curl -sS 'http://127.0.0.1:8090/firmwareimages/search?repository=127.0.0.1:5000/firmware/ilo&manufacturer=hpe' | jq
curl -sS 'http://127.0.0.1:8090/firmwareimages/image?repository=127.0.0.1:5000/firmware/ilo&tag=3.0.1' | jq
curl -i -X DELETE 'http://127.0.0.1:8090/firmwareimages/?repository=127.0.0.1:5000/firmware/ilo&tag=3.0.1'
```

To bulk-load the supplied catalog into the Compose registry, run from the repository root:

```bash
python tools/upload_images_to_registry.py \
  --images images.json \
  --payload-dir firmware \
  --repository registry:5000/firmware
```

The script creates zero-byte payload files for missing `s3URL` basenames. Use `--dry-run` to review those uploads without creating files or calling the API.

Use `FIRMWARE_UPDATER_QUAY_USERNAME` and `FIRMWARE_UPDATER_QUAY_PASSWORD` where the registry requires credentials. OCI connections use HTTPS in every mode, including loopback registries. Set `FIRMWARE_UPDATER_REPOSITORY_INSECURE_TLS=true` only to bypass certificate verification for a self-signed registry certificate; it does not downgrade the connection to HTTP.

## Follow-ups / Known Limits

- Upload currently reads the entire multipart payload into memory before pushing it to ORAS. A streaming ORAS upload should be considered before supporting consistently large production firmware images.
- Tests cover local filter/annotation/parameter behavior, but no end-to-end test against a live OCI registry has been added yet.
- Listing and searching fetch manifests sequentially. This favors straightforward behavior over throughput; bounded concurrency can be added when catalog sizes require it.
- No application-level authorization was added for upload or delete routes. Registry credentials control access to the upstream registry, but deployments exposing these routes should place them behind appropriate service authentication and authorization.
