# OCI Firmware Image API — Plan

## 1. Scope and Context

The firmware-updater currently only *reads* firmware artifacts from an OCI registry: `pkg/firmwareproxy/resolver.go` resolves an explicit OCI reference or discovers the best matching tag for a hardware model/version, and streams the payload layer back to a client. There is no API surface for:

1. **Storing** a firmware image into OCI storage (pushing a new artifact/tag).
2. **Listing** every firmware image currently stored in the registry.
3. **Searching** stored images by arbitrary metadata keys (e.g. `manufacturer`, `model`, `version`, `deviceType`) and returning every manifest that matches.

This plan adds a new package, `pkg/firmwareimages`, that owns push/list/search logic against the OCI registry using the existing ORAS (`oras.land/oras-go/v2`) dependency and annotation conventions already established in `firmwareproxy` (`org.opencontainers.image.version`, `org.opencontainers.image.title`, `dev.fabrica.hardware.compatible`). A new set of chi routes, `RegisterFirmwareImageRoutes`, exposes this package over HTTP, following the same manual-route pattern used by `cmd/server/deviceprofile_routes.go` (this is not a Fabrica-generated CRUD resource, since the "resource" lives in the OCI registry rather than in `internal/storage`).

## Implementation Updates

- OCI registry clients use HTTPS by default. `FIRMWARE_UPDATER_REPOSITORY_INSECURE_TLS=true` retains HTTPS but disables certificate verification for a self-signed registry certificate; it does not enable plain HTTP.
- The Compose deployment configures the updater with `FIRMWARE_UPDATER_REGISTRY_HOST=registry:5000`. Requests made from the updater container must use `registry:5000/firmware`; `localhost:5000` is only correct from the host machine.
- `tools/upload_images_to_registry.py` imports `images.json` through `POST /firmwareimages/`. It maps `target`, `models`, `softwareIds`, `firmwareVersion`, and `semanticFirmwareVersion` to API metadata; its default OCI tag is `imageID`. Missing local payloads create zero-byte files using the filename in `s3URL`, and `--dry-run` reports them without creating files.

## 2. Metadata / Annotation Model

Reuse and extend the existing annotation constants from `firmwareproxy` so both packages agree on the same manifest shape:

| Annotation key                        | Meaning                                   | Existing? |
|----------------------------------------|--------------------------------------------|-----------|
| `org.opencontainers.image.version`     | Firmware semantic version                  | Yes |
| `org.opencontainers.image.title`       | Original payload filename                  | Yes |
| `dev.fabrica.hardware.compatible`      | Comma-separated compatible hardware models | Yes |
| `dev.fabrica.hardware.manufacturer`    | Manufacturer (e.g. `hpe`, `cray`)           | New |
| `dev.fabrica.hardware.deviceType`      | Device type (e.g. `nodeBMC`)                | New |
| `dev.fabrica.firmware.tags`            | Comma-separated free-form tags              | New |
| `dev.fabrica.firmware.models`          | Comma-separated compatible platform models  | New |
| `dev.fabrica.firmware.softwareIds`     | Comma-separated firmware software IDs       | New |
| `dev.fabrica.firmware.versionString`   | Vendor-provided firmware version string     | New |

The new annotation keys are added as exported constants in `pkg/firmwareproxy/resolver.go` (or a shared `pkg/ociannotations` package if we want to avoid a new import from `firmwareimages` back into `firmwareproxy` internals — see Open Questions) so both push and resolve code paths stay in sync.

## 3. Package: `pkg/firmwareimages`

New files:
- `pkg/firmwareimages/store.go` — push logic.
- `pkg/firmwareimages/catalog.go` — list/search logic.
- `pkg/firmwareimages/types.go` — shared request/response structs.
- `pkg/firmwareimages/store_test.go`, `catalog_test.go` — unit tests (using an in-process ORAS memory store / local registry test double, matching patterns in `pkg/firmwareproxy/resolver_test.go`).

### 3.1 Types (`types.go`)

```go
// FirmwareImageMetadata describes a stored firmware artifact independent of
// how it was pushed or discovered.
type FirmwareImageMetadata struct {
    Repository         string   `json:"repository"`
    Tag                string   `json:"tag"`
    Digest             string   `json:"digest"`
    Version            string   `json:"version"`
    Filename           string   `json:"filename"`
    Targets            []string `json:"targets"`
    Models             []string `json:"models"`
    SoftwareIDs        []string `json:"softwareIds"`
    VersionString      string   `json:"versionString"`
    Manufacturer       string   `json:"manufacturer,omitempty"`
    DeviceType         string   `json:"deviceType,omitempty"`
    Tags               []string `json:"tags,omitempty"`
    SizeBytes          int64    `json:"sizeBytes"`
}

// PushRequest carries the metadata needed to store a firmware image.
// The binary payload itself is supplied as an HTTP multipart file part.
type PushRequest struct {
    Repository         string   `json:"repository"`
    Tag                string   `json:"tag"`
    Version            string   `json:"version"`
    Targets            []string `json:"targets"`
    Models             []string `json:"models"`
    SoftwareIDs        []string `json:"softwareIds"`
    VersionString      string   `json:"versionString"`
    Manufacturer       string   `json:"manufacturer,omitempty"`
    DeviceType         string   `json:"deviceType,omitempty"`
    Tags               []string `json:"tags,omitempty"`
}
```

### 3.2 Store / push (`store.go`)

```go
// PushFirmwareImage streams payload into the given OCI repository:tag,
// attaching the metadata annotations, and returns the resulting descriptor.
func PushFirmwareImage(ctx context.Context, req PushRequest, payload io.Reader, filename string, size int64) (FirmwareImageMetadata, error)
```

Implementation notes:
- Build a `firmwareproxy.NewRepository(req.Repository)` client so OCI API operations share registry authentication and transport configuration with the resolver.
- Use `oras.PushBytes` with a single blob layer of media type `application/octet-stream`, annotated with `org.opencontainers.image.title` = filename.
- Build the manifest with `ArtifactType: firmwareproxy.FirmwareBundleArtifactType` and manifest-level annotations: version, targets (stored in `dev.fabrica.hardware.compatible` for resolver compatibility), models, software IDs, version string, manufacturer, device type, and tags.
- Tag the manifest with `req.Tag` via `oras.Tag` / `PushBytes` options.
- Validate required fields (`Repository`, `Tag`, `Version`, at least one `Targets` entry) before touching the network; return `*firmwareproxy.HTTPStatusError{StatusCode: 400}` (or a local equivalent) for validation failures so the HTTP handler can map errors to status codes consistently with the resolver package.
- Package firmware metadata in an OCI image manifest with `oras.PackManifest` and `oras.PackManifestVersion1_1`; this keeps pushed artifacts compatible with the existing resolver.

### 3.3 List / search (`catalog.go`)

```go
// ListFirmwareImages enumerates every tag in every repository under the
// registry root (or a single repository if repository != "").
func ListFirmwareImages(ctx context.Context, registryHost, repository string) ([]FirmwareImageMetadata, error)

// SearchFirmwareImages returns every stored image whose annotations match
// all of the supplied key/value filters (AND semantics). Supported keys:
// "manufacturer", "deviceType", "model" (matches models), "target"
// (matches targets), "softwareId" (matches softwareIds), "version",
// "versionString", "tag" (free-form Tags field), "filename".
func SearchFirmwareImages(ctx context.Context, registryHost, repository string, filters map[string]string, latest bool) ([]FirmwareImageMetadata, error)

// DeleteFirmwareImage removes the firmware bundle manifest selected by tag.
// It refuses to delete a manifest referenced by another tag unless force is true.
func DeleteFirmwareImage(ctx context.Context, repository, tag string, force bool) error
```

When the HTTP query parameter `latest=true` is supplied, search returns only the image with the highest valid semantic `version` among the filtered results. It may be used without another filter to return the latest valid-version image in the scoped repository/registry. Invalid semantic versions are excluded from latest selection; equal versions are resolved deterministically by OCI tag.

Implementation notes:
- `ListFirmwareImages` uses `remote.NewRegistry(registryHost)` + `.Repositories(ctx, "", fn)` to enumerate repositories when `repository == ""`, otherwise scopes to the single repository.
- For each repository, list tags (`repo.Tags`) and fetch each manifest (`oras.FetchBytes`), decoding into `ocispec.Manifest` and skipping anything whose `ArtifactType` isn't `FirmwareBundleArtifactType` (mirrors `buildManifestCandidate` in `resolver.go`).
- Convert each matching manifest into a `FirmwareImageMetadata` (reusing an extraction helper shared with `store.go`/`resolver.go` where practical).
- Concurrency: fetch manifests for tags within a repository with a small bounded worker pool (existing code is currently sequential; keep sequential for the first version and note this as a future optimization rather than over-engineering it up front).
- `SearchFirmwareImages` calls `ListFirmwareImages` and applies an in-memory filter: a candidate matches only if every supplied filter key is present and satisfied (case-insensitive substring match for `filename`/`model`/`tag`, exact case-insensitive match for `manufacturer`/`deviceType`/`version`). Returns an empty slice (not an error) when nothing matches.
- `DeleteFirmwareImage` resolves `repository:tag`, verifies that the manifest is a `FirmwareBundleArtifactType`, then deletes that manifest by digest through the registry's OCI Distribution API client. Repository and tag are required; an unknown repository/tag or a non-firmware manifest returns `404`.
- OCI registries delete manifests by digest rather than treating a tag as an independent resource. Before deletion, enumerate tags in the repository and identify every tag resolving to the selected digest. If another tag references it, return `409 Conflict` with those tags unless `force=true`; forced deletion removes the shared manifest and therefore every tag pointing at it. A successful delete returns `204 No Content`. Blob garbage collection is registry-managed and is not triggered by this API.

## 4. HTTP Routes (`cmd/server/firmwareimage_routes.go`)

Follows the `deviceprofile_routes.go` pattern: plain chi handlers registered from `main.go` after `RegisterGeneratedRoutes(r)`.

All successful image responses use the Fabrica resource shape with `apiVersion`, `kind`, `metadata`, `spec`, and `status`. List and search responses return arrays of `FirmwareImage` resources, matching the generated Fabrica list behavior rather than a custom `{items,count}` envelope.

```go
func RegisterFirmwareImageRoutes(r chi.Router) {
    r.Route("/firmwareimages", func(r chi.Router) {
        r.Get("/", listFirmwareImagesHandler)     // GET  /firmwareimages?repository=...
        r.Get("/search", searchFirmwareImagesHandler) // GET /firmwareimages/search?model=...&manufacturer=...
        r.Get("/image", getFirmwareImageHandler)  // GET /firmwareimages/image?repository=...&tag=...
        r.Post("/", pushFirmwareImageHandler)      // POST /firmwareimages (multipart/form-data)
        r.Delete("/", deleteFirmwareImageHandler)  // DELETE /firmwareimages?repository=...&tag=...&force=false
    })
}
```

Endpoint details:

- **`POST /firmwareimages`** — multipart form with a `metadata` part (JSON body matching `PushRequest`) and a `file` part (the firmware binary). Handler decodes metadata, reads the file part into an `io.Reader` with known size (`multipart.FileHeader.Size`), calls `firmwareimages.PushFirmwareImage`, and returns `201 Created` with the resulting `FirmwareImageMetadata`. Validation and registry errors map through the existing `*firmwareproxy.HTTPStatusError` → chi status-code convention (400/404/409/502 as appropriate).
- **`GET /firmwareimages`** — optional `?repository=` query param to scope to one repository; otherwise lists everything under the configured registry root. Returns `{ "items": [...], "count": N }`, same envelope style as `deviceProfileListResponse`.
- **`GET /firmwareimages/search`** — supported query filters are `manufacturer`, `deviceType`, `model`, `target`, `softwareId`, `version`, `versionString`, `tag`, and `filename`. Filters use AND semantics. `latest=true` optionally returns only the highest valid semantic version after filtering, and may be used by itself. Returns the same list envelope containing every matching image.
- **`DELETE /firmwareimages?repository={repository}&tag={tag}&force={bool}`** — removes the selected firmware image manifest. `repository` and `tag` are mandatory query parameters so OCI repository paths containing `/` need no special path encoding. It returns `204 No Content` on success, `400` for missing/invalid parameters, `404` when the selected tag is absent or not a firmware bundle, and `409` when the manifest is shared by another tag and `force` is absent/false. `force=true` explicitly accepts deletion of every tag resolving to that digest.
- **`GET /firmwareimages/image?repository={repository}&tag={tag}`** — fetches a single manifest and returns its `FirmwareImageMetadata`; query parameters allow OCI repository names containing `/` without ambiguous route parsing. Returns `404` if the tag/repository doesn't exist or isn't a firmware bundle artifact.

Registry host/root used by list/search is configured through `Config.RegistryHost`, available as `--registry-host` or `FIRMWARE_UPDATER_REGISTRY_HOST`.

## 5. Wiring (`cmd/server/main.go`)

- Add `RegisterFirmwareImageRoutes(r)` next to the existing `RegisterDeviceProfileRoutes(r)` call.
- No new storage/migrations/reconciler changes needed — this feature is registry-backed, not database-backed.

## 6. Error Handling

Reuse `firmwareproxy.HTTPStatusError` and `classifyORASError` (export/reuse rather than duplicate) so registry connectivity failures, auth failures, and not-found cases map to the same HTTP status codes already used by the discovery/resolve endpoints (400 bad request, 404 not found, 502 upstream registry error).

## 7. Testing

- `pkg/firmwareimages/store_test.go`: push a small in-memory payload against an ORAS `memory.Store`-backed or local test registry, assert the manifest annotations and returned `FirmwareImageMetadata` are correct.
- `pkg/firmwareimages/catalog_test.go`: seed a test registry with several tagged manifests (different manufacturers/models/versions) and assert `ListFirmwareImages` returns all of them and `SearchFirmwareImages` correctly filters by each supported key and by combinations of keys (AND semantics), including a case with zero matches.
- `pkg/firmwareimages/catalog_test.go`: verify deletion removes a uniquely tagged firmware manifest, missing tags return `404`, non-firmware manifests cannot be deleted through this endpoint, and a shared digest produces `409` unless `force=true`.
- `cmd/server/firmwareimage_routes_test.go`: verify the delete handler validates `repository`, `tag`, and `force`, and returns the package error status without a response body on `204`.
- `go test ./pkg/firmwareimages ./cmd/server` for unit coverage of handlers (using `httptest`).

## 8. Manual End-to-End Validation

1. Start a local registry (`registry-config.yml` already defines one on `:5000`) and the firmware-updater server.
2. Push an image:
   ```bash
   curl -sS -X POST http://127.0.0.1:8090/firmwareimages \
    -F 'metadata={"repository":"127.0.0.1:5000/firmware/ilo","tag":"3.0.1","version":"3.0.1","targets":["iLO 5"],"models":["ProLiant DL325 Gen10"],"softwareIds":["ilo5"],"versionString":"3.0.1 (vendor)","manufacturer":"hpe","deviceType":"nodeBMC"};type=application/json' \
     -F 'file=@./dummy-firmware.bin;type=application/octet-stream'
   ```
3. List all images: `curl -sS http://127.0.0.1:8090/firmwareimages | jq`.
4. Search by key: `curl -sS 'http://127.0.0.1:8090/firmwareimages/search?manufacturer=hpe' | jq` and confirm the pushed image is present; try a non-matching filter and confirm an empty `items` array. For the latest matching version: `curl -sS 'http://127.0.0.1:8090/firmwareimages/search?softwareId=ilo5&latest=true' | jq`.
5. Fetch a single image: `curl -sS 'http://127.0.0.1:8090/firmwareimages/image?repository=127.0.0.1:5000/firmware/ilo&tag=3.0.1' | jq`.
6. Delete it: `curl -i -X DELETE 'http://127.0.0.1:8090/firmwareimages?repository=127.0.0.1:5000/firmware/ilo&tag=3.0.1'`; confirm the response is `204 No Content`.
7. List or fetch the deleted tag and confirm it returns no matching image / `404`. Repeat deletion and confirm it returns `404`.

## 9. Acceptance Criteria

- Code compiles (`go build ./...`) and `go vet ./...` is clean.
- New unit tests pass (`go test ./pkg/firmwareimages ./cmd/server`).
- Pushing an image via the API makes it retrievable via both `GET /firmwareimages` and the existing `firmwareproxy` discovery/resolve paths (i.e. push output is a valid `FirmwareBundleArtifactType` manifest compatible with current consumers).
- Search returns all matching images (not just the first) and applies AND semantics across multiple filter keys.
- Deleting a uniquely tagged firmware image via `DELETE /firmwareimages` makes it unavailable to list, search, fetch, and resolver discovery.
- Deletion detects shared manifest digests and returns `409` by default; `force=true` deletes the shared manifest only when this effect is explicit.
- Missing/invalid push metadata returns `400`; registry/network failures return `502`/`404` as appropriate rather than a generic `500`.

## 10. Open Questions

- Should compatible-hardware/manufacturer/deviceType/tags annotation constants live in a new shared `pkg/ociannotations` package to avoid `firmwareimages` importing unexported helpers from `firmwareproxy`? (Recommended: yes, extract shared constants/helpers such as `applyRepoAuth`, `isLoopbackRegistry`, `classifyORASError`, and annotation keys into a small shared package, e.g. `pkg/ocicommon`, imported by both `firmwareproxy` and `firmwareimages`.)
- Does push need authentication/authorization beyond existing registry credentials (e.g. an API token for the firmware-updater's own HTTP endpoint)? Out of scope for this plan unless required by a follow-up security review.
