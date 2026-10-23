# Credential Resolution Rework Handoff

Implements [UPDATE_CREDENTIALS.md](UPDATE_CREDENTIALS.md).

## 1. Implementation Summary

- `secretID` is now **optional** on `FirmwareUpdateJob.spec` and on
  `FirmwareUpdateCampaign.spec.targets[]`.
- Credential selection goes through an ordered chain in the new `pkg/credentials`
  package. The first rule that produces a secret ID wins:

  | # | Rule | `Source` | Input |
  | --- | --- | --- | --- |
  | 1 | Explicit | `explicit` | `spec.secretID` / `spec.targets[].secretID` |
  | 2 | Binding | `binding` | a matching entry in `spec.credentials.bindings` |
  | 3 | Node name | `node-name` | a secret stored under the node's `targetAddress` |
  | 4 | Spec default | `spec-default` | `spec.credentials.default` |
  | 5 | Server default | `server-default` | `--default-secret-id` |

- A new optional `credentials` section on jobs and campaigns maps node names (or
  glob patterns) to secret IDs and supplies a local default.
- A new `--default-secret-id` / `FIRMWARE_UPDATER_DEFAULT_SECRET_ID` server
  option supplies the fleet-wide last resort.
- **Strictness:** a rule that names a secret which is not in the store is a hard
  400 failure, not a fall-through. Resolution can never silently pick a
  different credential because of a typo. The only non-strict rule is node-name
  lookup, which is a probe by definition.
- **Campaign → job propagation decision.** The plan offered two options. We
  propagate the campaign's `credentials` section down to each child job rather
  than resolving at campaign build time. Reason: the child-job builder
  (`campaignToChildJob`) is a pure function with no secret-store access, and
  resolving there would fail an entire campaign because of one bad target.
  Auditability is preserved instead through the new job status fields, which
  record the outcome per child job.

## 2. Technical Details

### New API types

`apis/hardware.fabrica.dev/v1/credentials_types.go`

```go
type CredentialBinding struct {
    Nodes    []string `json:"nodes"`
    SecretID string   `json:"secretID"`
}

type CredentialsSpec struct {
    Bindings []CredentialBinding `json:"bindings,omitempty"`
    Default  string              `json:"default,omitempty"`
}
```

- `CredentialsSpec.Validate(fieldPath)` rejects empty `secretID`, empty `nodes`,
  empty node entries, and malformed glob patterns. A nil receiver is valid.
- `CredentialsSpec.MatchNode(nodeName)` does an exact case-insensitive pass over
  all bindings first, then a `path.Match` glob pass. Within each pass the first
  matching binding wins, so an exact match always beats a glob regardless of
  declaration order.

### Changed API types

| File | Change |
| --- | --- |
| `apis/.../firmwareupdatejob_types.go` | `SecretID` is `json:"secretID,omitempty"` with no `validate:"required"`; added `Credentials *CredentialsSpec`; added `Status.ResolvedSecretID` and `Status.CredentialSource`; `Validate` calls `Spec.Credentials.Validate` |
| `apis/.../firmwareupdatecampaign_types.go` | `FirmwareCampaignTarget.SecretID` optional; added `Spec.Credentials`; removed the per-target "secretID must be provided" check; `Validate` calls `Spec.Credentials.Validate` |

### New resolver package

`pkg/credentials/resolver.go`

```go
func Resolve(store SecretLookup, in ResolveInput) (Resolution, error)
func Exists(store SecretLookup, secretID string) bool
func SetServerDefaultSecretID(secretID string)
func ServerDefaultSecretID() string
```

- `SecretLookup` is a one-method interface (`GetSecretByID`) satisfied by the
  Magellan `secrets.SecretStore`, so the resolver is unit-testable without the
  encrypted store.
- Failures are `*firmwareproxy.HTTPStatusError{StatusCode: 400}` so the existing
  `isTerminalError` treats them as terminal and the job fails fast instead of
  retrying forever. An uninitialized store returns a plain error, which stays
  transient/retryable.
- `Exists` treats both a lookup error and a blank payload as "absent" because
  the on-disk store and the test fakes report a miss differently.
- **Deviation from the plan:** the existence probe lives in `pkg/credentials` as
  `Exists(store, id)` rather than as `secretsruntime.Exists(id)`. Keeping it on
  the narrow `SecretLookup` interface avoids duplicating the "what counts as
  present" rule in two packages, and callers that want the global store pass
  `secretsruntime.GetStore()`.

### Reconciler wiring

`pkg/reconcilers/firmwareupdatejob_reconciler.go`

```go
func resolveBMCCredentials(nodeName, explicitID string, spec *v1.CredentialsSpec) (bmcCredentials, credentials.Resolution, error)
```

Resolves the secret ID, then decodes it through the unchanged
`loadBMCCredentials`. Call sites updated:

| Location | Change |
| --- | --- |
| `reconcileFirmwareUpdateJob` | resolves from `Spec.TargetAddress`/`Spec.SecretID`/`Spec.Credentials`; writes `Status.ResolvedSecretID` and `Status.CredentialSource` |
| `observeInProgressFirmwareUpdateJob` | same |
| `discoverTargetComponentUpdates` (campaign) | resolves from `target.TargetAddress`/`target.SecretID`/`campaign.Spec.Credentials` |
| `campaignToChildJob` | copies `campaign.Spec.Credentials` onto the child job spec |

### Server configuration

`cmd/server/main.go`

- `Config.DefaultSecretID` (`mapstructure:"default-secret-id"`), default `""`.
- Flag `--default-secret-id`, env `FIRMWARE_UPDATER_DEFAULT_SECRET_ID`.
- `initializeSecretStore(secretFile, defaultSecretID)` registers the default with
  `credentials.SetServerDefaultSecretID` and logs whether the secret is present.
  A missing default warns rather than aborting startup, so the secret can be
  added to the store afterwards.

### Logging and redaction

No new log statement prints credential material; only secret IDs and the
resolution source are logged or persisted. `pkg/debug/sanitize.go` already
redacts any key containing `secret`, so `resolvedSecretID` is redacted in debug
output without further changes.

### Code generation

API type edits normally require:

```bash
fabrica generate
go generate ./...
go build ./...
```

**Known issue:** the `fabrica` binary installed in the development environment
used for this change fails with
`failed to read embedded template templates\apiversion\types_spoke.gotmpl: file does not exist`
and aborts partway through, so generated artifacts were **not** refreshed. This
is a pre-existing tooling problem unrelated to the credential work. The impact
is limited and non-blocking:

- `pkg/client/models_generated.go` embeds the `v1` spec types directly, so the
  client already exposes the new fields.
- `cmd/server/openapi_generated.go` and `apis.yaml` do not enumerate `secretID`,
  so they need no field-level edit.
- `cmd/client/main.go` (generated, `DO NOT EDIT`) still prints
  `secretID (string) [required]` in its `--help` spec-field list. This is a
  cosmetic staleness. **Re-run `fabrica generate` once the CLI is repaired** and
  confirm the help text and OpenAPI output pick up `credentials`,
  `resolvedSecretID`, and `credentialSource`.

Note that the failed run also rewrote `pkg/resources/register_generated.go` with
an unrelated change; that file was reverted.

### Backward compatibility

Existing payloads are unaffected: an explicit `secretID` is still rule 1.
Relaxing `validate:"required"` cannot invalidate any stored resource, and specs
are persisted as `json.RawMessage` in the generic Ent `Resource` entity, so no
storage migration is required.

## 3. Verified Testing Evidence

```
go build ./...
go test ./pkg/credentials/... ./apis/... ./pkg/reconcilers/...
ok  github.com/user/firmware-updater/pkg/credentials
ok  github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1
```

`pkg/reconcilers` has three **pre-existing** failures unrelated to this change:
two require cgo for SQLite (`CGO_ENABLED=0` in this environment) and one asserts
`omitempty` behaviour on `CampaignChildJob`, a type this change does not touch.
All credential tests pass:

```
go test ./pkg/reconcilers/... -run "Credential|ResolveBMC|CampaignToChildJob" -v
--- PASS: TestResolveBMCCredentialsChain
    --- PASS: .../explicit_secretID
    --- PASS: .../credentials_binding
    --- PASS: .../node_name_lookup
    --- PASS: .../spec_default
    --- PASS: .../server_default
--- PASS: TestResolveBMCCredentialsUnresolvedIsTerminal
--- PASS: TestResolveBMCCredentialsRejectsIncompleteSecret
--- PASS: TestCampaignToChildJobInheritsCampaignCredentials
--- PASS: TestCampaignToChildJob_PropagatesDryRun
```

### Test inventory

| File | Covers |
| --- | --- |
| `apis/.../credentials_types_test.go` | nil/empty section valid; missing `secretID`; empty `nodes`; blank node entry; malformed glob; exact beats glob; glob match; case-insensitive match; first-glob-wins; empty node name; nil receiver; no bindings |
| `pkg/credentials/resolver_test.go` | explicit wins over everything; missing explicit ID is terminal and does not fall back; exact beats glob; binding order between globs; binding beats node name; missing binding secret is terminal; node-name hit; spec default beats server default; server default; nothing configured; malformed glob; nil store is non-terminal; `Exists` table (present/trimmed/absent/blank/empty/nil); server-default round trip |
| `pkg/reconcilers/credential_resolution_test.go` | all five rules end-to-end through the real global store; unresolved input is terminal; a secret with blank username/password is rejected; child jobs inherit the campaign `credentials` section |

### Manual verification payloads

Store a node-named secret and a fleet default:

```bash
export MASTER_KEY="$(openssl rand -hex 32)"
go run ./cmd/secret-cli --secret-id x9000c3s7b1 --username root --password initial0 --store-path ./secrets.json
go run ./cmd/secret-cli --secret-id fleet-bmc    --username root --password initial0 --store-path ./secrets.json
go run ./cmd/secret-cli --secret-id lab-bmc      --username root --password initial0 --store-path ./secrets.json

go run ./cmd/server serve \
  --port 8090 \
  --database-url="file:hpc_test.db?cache=shared&_fk=1" \
  --secrets-file ./secrets.json \
  --default-secret-id fleet-bmc
```

Campaign exercising four of the five rules at once:

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatecampaigns \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": { "name": "credential-chain-demo" },
    "spec": {
      "serverProxyAddress": "10.254.1.20",
      "credentials": {
        "bindings": [ { "nodes": ["x3000*"], "secretID": "lab-bmc" } ]
      },
      "targets": [
        { "targetAddress": "x9000c3s7b2", "secretID": "lab-bmc" },
        { "targetAddress": "x3000c0s21b0" },
        { "targetAddress": "x9000c3s7b1" },
        { "targetAddress": "unknown-node" }
      ],
      "discovery": { "repository": "127.0.0.1:5000/firmware" }
    }
  }'
```

Expected `status.credentialSource` on the child jobs, in target order:
`explicit`, `binding`, `node-name`, `server-default`.

Inspect the outcome per job:

```bash
curl -sS http://127.0.0.1:8090/firmwareupdatejobs | \
  jq '.items[] | {node: .spec.targetAddress, secret: .status.resolvedSecretID, via: .status.credentialSource}'
```

## 4. Usage Notes

**Store a secret named after the node** and omit `secretID` from payloads:

```bash
go run ./cmd/secret-cli --secret-id <targetAddress> --username root --password <pw> --store-path ./secrets.json
```

**Set the fleet default** with `--default-secret-id <id>` or
`FIRMWARE_UPDATER_DEFAULT_SECRET_ID=<id>`. A missing default only warns at
startup.

**Write a `credentials` section** when node names and secret IDs differ:

```json
"credentials": {
  "bindings": [
    { "nodes": ["x9000c3s7b1"], "secretID": "lab-bmc" },
    { "nodes": ["x9000*", "x3000*"], "secretID": "x9000-bmc" }
  ],
  "default": "fallback-bmc"
}
```

Bindings are evaluated in declaration order and the first match wins, except
that an exact node-name match always beats a glob. Matching is case-insensitive
and patterns use `*`, `?`, `[...]` (`path.Match` semantics).

**Debugging an unresolved credential.** Read `status.errorDetail` on the job.
The three distinguishable messages are:

| Message | Meaning |
| --- | --- |
| `no credential could be resolved for node "..."` | every rule came up empty — store a node-named secret, add a binding, or set a default |
| `spec.secretID selected secretID "..." which is not present in the secret store` | typo, or the secret was never written with the current `MASTER_KEY` |
| `credentials binding for node "..." selected secretID "..." which is not present in the secret store` | the binding matched but points at a missing secret |

`status.resolvedSecretID` and `status.credentialSource` on a successful job show
exactly which secret was used and which rule chose it. If a job picks an
unexpected credential, check `credentialSource` first: `node-name` means a
secret exists under the node's address and is shadowing your intended default.

## 5. Acceptance Criteria

- [x] `secretID` is optional on both `FirmwareUpdateJob` and campaign targets.
- [x] Optional `credentials` section accepted on both jobs and campaigns.
- [x] Node-name lookup works when no explicit ID and no binding match.
- [x] Server-wide default credential configurable and used as last resort.
- [x] An explicit but unresolvable `secretID` is a hard error (no silent fallback).
- [x] Resolution source is visible in job status.
- [x] No credential values in logs, errors, or API responses.
- [x] Existing job/campaign payloads continue to work with no edits.
- [ ] Generated code regenerated and committed — **blocked** on the broken
      `fabrica` binary described above; no functional impact, help text only.
- [x] Documentation updated (`README.md`, `docs/user-guide.md`,
      `planning/secrets.md`).
- [x] `planning/HANDOFF-update_credentials.md` created.
