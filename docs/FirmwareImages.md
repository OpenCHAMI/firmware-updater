# Firmware Images

This guide describes how to store, inspect, search, import, and delete firmware images through the firmware-updater API.

## Overview

Firmware images are stored in an OCI registry. The firmware-updater does not maintain a second firmware-image database in SQLite. Each uploaded image is an OCI image manifest with artifact type:

```text
application/vnd.openchami.firmware.bundle.v1+json
```

The API is available under `/firmwareimages`.

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/firmwareimages/` | Upload a firmware payload and metadata. |
| `GET` | `/firmwareimages/?repository=...` | List images in one repository. |
| `GET` | `/firmwareimages/search?...` | Search image metadata. |
| `GET` | `/firmwareimages/image?repository=...&tag=...` | Get one image's metadata. |
| `DELETE` | `/firmwareimages/?repository=...&tag=...&force=false` | Delete an image manifest. |

Individual image responses use the standard Fabrica resource shape, and list/search responses return an array of these resources:

```json
{
  "apiVersion": "hardware.fabrica.dev/v1",
  "kind": "FirmwareImage",
  "metadata": {
    "name": "registry:5000/firmware/ilo:3.0.1",
    "uid": "sha256:...",
    "createdAt": "2026-08-26T12:00:00Z",
    "updatedAt": "2026-08-26T12:00:00Z"
  },
  "spec": {
    "repository": "registry:5000/firmware/ilo",
    "tag": "3.0.1",
    "version": "3.0.1",
    "filename": "dummy-firmware.bin",
    "targets": ["iLO 5"],
    "models": ["ProLiant DL325 Gen10"],
    "softwareIds": ["ilo5"],
    "versionString": "3.0.1 (vendor)",
    "manufacturer": "hpe",
    "deviceType": "nodeBMC",
    "tags": ["production"]
  },
  "status": {
    "digest": "sha256:...",
    "sizeBytes": 0
  }
}
```

## Registry Configuration

Start the service with a registry root when you want list or search requests without an explicit `repository` parameter:

```bash
go run ./cmd/server serve \
  --port 8090 \
  --registry-host registry.example.com \
  --database-url="file:hpc.db?cache=shared&_fk=1" \
  --secrets-file ./secrets.json
```

The equivalent environment variable is:

```bash
export FIRMWARE_UPDATER_REGISTRY_HOST=registry.example.com
```

Every repository value must contain both a registry host and a repository path, such as `registry.example.com/firmware` or `registry:5000/firmware`. A value such as `registry:5000` is only a registry host and is not a complete repository.

### Docker Compose

Inside the Docker Compose network, the registry service is named `registry`:

```text
registry:5000/firmware
```

From the host machine, use the published port:

```text
localhost:5000/firmware
```

`localhost` inside the firmware-updater container refers to that container itself, not the registry container.

OCI connections use HTTPS by default. Set the following only when the registry uses a self-signed certificate:

```bash
export FIRMWARE_UPDATER_REPOSITORY_INSECURE_TLS=true
```

This setting keeps HTTPS but disables certificate verification. It does not switch OCI communication to HTTP. The Compose test registry already uses TLS and sets this option for the updater service.

Registry credentials are supplied with:

```bash
export FIRMWARE_UPDATER_QUAY_USERNAME="username"
export FIRMWARE_UPDATER_QUAY_PASSWORD="password-or-token"
```

Credentials are used for outbound OCI requests and are not stored in firmware image metadata.

## Create an Image

### Request format

`POST /firmwareimages/` requires `multipart/form-data` with two parts:

- `metadata`: JSON object containing the image metadata.
- `file`: the firmware binary.

Required metadata fields:

- `repository`: complete OCI repository, including host and path.
- `tag`: OCI tag to assign to the manifest.
- `version`: semantic version used by resolver discovery and latest-version search.
- `targets`: one or more target component names. This is the public replacement for `compatibleHardware`.
- `file`: non-empty filename supplied by the multipart file part.

Optional metadata fields:

- `models`: hardware platform model names.
- `softwareIds`: firmware software identifiers or patterns.
- `versionString`: vendor-provided human-readable version string.
- `manufacturer`: hardware manufacturer.
- `deviceType`: device category.
- `tags`: free-form image tags.

### Example upload

```bash
curl -sS -X POST http://localhost:8080/firmwareimages/ \
  -F 'metadata={"repository":"registry:5000/firmware/ilo","tag":"3.0.1","version":"3.0.1","targets":["iLO 5"],"models":["ProLiant DL325 Gen10"],"softwareIds":["ilo5"],"versionString":"3.0.1 (vendor)","manufacturer":"hpe","deviceType":"nodeBMC","tags":["production"]};type=application/json' \
  -F 'file=@./dummy-firmware.bin;type=application/octet-stream'
```

A successful upload returns `201 Created` with a Fabrica `FirmwareImage` resource:

```json
{
  "apiVersion": "hardware.fabrica.dev/v1",
  "kind": "FirmwareImage",
  "metadata": {"name": "registry:5000/firmware/ilo:3.0.1", "uid": "sha256:..."},
  "spec": {"repository": "registry:5000/firmware/ilo", "tag": "3.0.1", "version": "3.0.1", "targets": ["iLO 5"]},
  "status": {"digest": "sha256:...", "sizeBytes": 0}
}
```

The filename is stored on the first OCI payload layer as `org.opencontainers.image.title`. The API reads the payload into memory before pushing it; the HTTP handler limits uploads to 2 GiB.

## OCI Metadata

The API maps public metadata to OCI annotations as follows:

| Public field | OCI annotation | Notes |
|---|---|---|
| `version` | `org.opencontainers.image.version` | Must be a valid semantic version for discovery. |
| `targets` | `dev.fabrica.hardware.compatible` | Comma-separated for compatibility with the existing resolver. |
| `models` | `dev.fabrica.firmware.models` | Comma-separated. |
| `softwareIds` | `dev.fabrica.firmware.softwareIds` | Comma-separated. |
| `versionString` | `dev.fabrica.firmware.versionString` | Preserves the vendor string. |
| `manufacturer` | `dev.fabrica.hardware.manufacturer` | Exact-match search field. |
| `deviceType` | `dev.fabrica.hardware.deviceType` | Exact-match search field. |
| `tags` | `dev.fabrica.firmware.tags` | Comma-separated free-form values. |
| file filename | `org.opencontainers.image.title` on the layer | Identifies the payload file. |

The first layer must contain the firmware payload. Existing discovery requires the expected artifact type, at least one layer, a valid semantic version annotation, and a compatible target annotation.

## List Images

List one repository:

```bash
curl -sS "http://localhost:8080/firmwareimages/?repository=registry:5000/firmware" | jq
```

List every firmware image in every repository under the configured registry host:

```bash
curl -sS "http://localhost:8080/firmwareimages/" | jq
```

The all-repositories form requires `--registry-host` or `FIRMWARE_UPDATER_REGISTRY_HOST`. Non-firmware manifests are skipped.

## Search Images

Search accepts these query parameters and returns an array of Fabrica `FirmwareImage` resources:

| Parameter | Matches | Comparison |
|---|---|---|
| `manufacturer` | `manufacturer` | Case-insensitive exact match. |
| `deviceType` | `deviceType` | Case-insensitive exact match. |
| `version` | semantic `version` | Case-insensitive exact match. |
| `model` | Any value in `models` | Case-insensitive substring match. |
| `target` | Any value in `targets` | Case-insensitive substring match. |
| `softwareId` | Any value in `softwareIds` | Case-insensitive substring match. |
| `versionString` | `versionString` | Case-insensitive substring match. |
| `tag` | Any free-form value in `tags` | Case-insensitive substring match. |
| `filename` | payload filename | Case-insensitive substring match. |

Multiple search parameters use AND semantics. Use `&` between parameters, not a second `?`:

```bash
curl -sS "http://localhost:8080/firmwareimages/search?repository=registry:5000/firmware&version=2.1.0-59&softwareId=sc:*:*:*" | jq
```

A search must contain at least one supported filter unless `latest=true` is supplied. Unknown query parameters are ignored by the HTTP handler.

### Latest matching image

Set `latest=true` to return only the image with the highest valid semantic `version` after all other filters are applied. It can be used by itself to find the newest valid image in a repository:

```bash
curl -sS "http://localhost:8080/firmwareimages/search?repository=registry:5000/firmware&softwareId=sc:*:*:*&latest=true" | jq
```

Invalid semantic versions are excluded from latest selection. If multiple images have the same version, the OCI tag provides a deterministic tie-breaker. The response remains a list envelope, with zero or one item.

## Get One Image

Use the query form because OCI repository names commonly contain `/`:

```bash
curl -sS "http://localhost:8080/firmwareimages/image?repository=registry:5000/firmware/ilo&tag=3.0.1" | jq
```

The endpoint returns `404 Not Found` when the tag is absent or does not identify a firmware bundle.

## Delete an Image

Delete a uniquely tagged image:

```bash
curl -i -X DELETE "http://localhost:8080/firmwareimages/?repository=registry:5000/firmware/ilo&tag=3.0.1"
```

A successful deletion returns `204 No Content`. OCI deletion occurs by manifest digest. If another tag points to the same digest, the API returns `409 Conflict` and leaves the manifest unchanged:

```bash
curl -i -X DELETE "http://localhost:8080/firmwareimages/?repository=registry:5000/firmware/ilo&tag=3.0.1&force=false"
```

To explicitly delete a shared manifest and all tags pointing to it:

```bash
curl -i -X DELETE "http://localhost:8080/firmwareimages/?repository=registry:5000/firmware/ilo&tag=3.0.1&force=true"
```

Registry blob garbage collection is managed by the registry and is not triggered by this API.

## Import `images.json`

The repository includes [upload_images_to_registry.py](../tools/upload_images_to_registry.py) for importing the catalog in `images.json`.

The importer maps:

| `images.json` field | API field |
|---|---|
| `semanticFirmwareVersion` | `version` |
| `target` | `targets` as a one-item array |
| `models` | `models` |
| `softwareIds` | `softwareIds` |
| `firmwareVersion` | `versionString` |
| `manufacturer` | `manufacturer` |
| `deviceType` | `deviceType` |
| `tags` | `tags` |
| `imageID` | default OCI tag |
| filename from `s3URL` | multipart file filename |

Run it from the repository root:

```bash
python tools/upload_images_to_registry.py \
  --images images.json \
  --payload-dir firmware \
  --repository registry:5000/firmware
```

The importer expects payloads in `--payload-dir` named after the basename of each `s3URL`. If a file is missing, it creates a zero-byte placeholder with that filename and uploads it. It does not download from S3.

Preview the operation without creating files or making API requests:

```bash
python tools/upload_images_to_registry.py \
  --images images.json \
  --payload-dir firmware \
  --repository registry:5000/firmware \
  --dry-run
```

Use `--continue-on-error` to process every catalog entry after an individual error. The importer rejects incomplete repository values such as `registry:5000`; include a path such as `/firmware`.

## HTTP Statuses

| Status | Meaning |
|---|---|
| `201` | Image uploaded. |
| `204` | Image manifest deleted. |
| `400` | Invalid metadata, repository, query parameter, or request. |
| `404` | Repository/tag/image was not found. |
| `409` | Delete was blocked because the manifest is shared by another tag. |
| `503` | Registry connectivity or upstream registry failure. |

## Troubleshooting

### `at least one search filter is required`

Use a supported filter, or add `latest=true`:

```text
/search?softwareId=sc:*:*:*&latest=true
```

Make sure multiple parameters are separated with `&`.

### `repository must include both registry host and repository path`

Use `registry:5000/firmware`, not `registry:5000`.

### Connection refused on `localhost:5000`

When the updater runs in Docker Compose, use `registry:5000/firmware`. `localhost` resolves inside the updater container and does not refer to the registry service.

### TLS or certificate errors

Use a certificate trusted by the updater in normal operation. For the Compose self-signed test certificate, set `FIRMWARE_UPDATER_REPOSITORY_INSECURE_TLS=true`. This keeps HTTPS while bypassing certificate verification.
