# Credential Resolution Rework — Implementation Plan

## 1. Goal

Today every `FirmwareUpdateJob` and every campaign target **must** carry an explicit
`secretID` (`apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go`,
`apis/hardware.fabrica.dev/v1/firmwareupdatecampaign_types.go`). That is brittle for
large fleets where credentials are per-node or fleet-wide.

Change credential selection to a layered resolution chain:

1. **Explicit `secretID`** on the job / campaign target (unchanged behavior, now optional).
2. **`credentials` section** in the job or campaign spec — an optional list mapping node
   names (or patterns) to a secret ID.
3. **Node-name lookup** — use the node name (`targetAddress`) as the secret ID.
4. **Default credential** — a configured fallback secret ID.
5. Otherwise: fail with a clear, actionable error.

## 2. Current State (reference)

| Concern | Location |
| --- | --- |
| Job spec `SecretID` (`validate:"required"`) | `apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go` |
| Campaign target `SecretID` (`validate:"required"`) | `apis/hardware.fabrica.dev/v1/firmwareupdatecampaign_types.go` |
| `loadBMCCredentials(secretID)` | `pkg/reconcilers/firmwareupdatejob_reconciler.go` |
| Call sites | job reconciler (initial + in-progress), `pkg/reconcilers/firmwareupdatecampaign_reconciler.go` |
| Global secret store accessor | `internal/secretsruntime/store.go` |
| Store wiring, `--secrets-file`, `MASTER_KEY` | `cmd/server/main.go` |
| Secret creation CLI | `cmd/secret-cli/main.go` |

Secrets live in an encrypted JSON file keyed by secret ID; values are
`{"username":"...","password":"..."}`.

## 3. API Changes

### 3.1 New shared type

Add to `apis/hardware.fabrica.dev/v1/` (new file `credentials_types.go`):

```go
// CredentialBinding maps one or more node names to a stored secret ID.
type CredentialBinding struct {
	// Nodes lists node names (targetAddress values) this binding applies to.
	// Entries may be exact names or glob patterns (e.g. "x9000-*").
	Nodes []string `json:"nodes" validate:"required,min=1,dive,required"`

	// SecretID is the stored credential to use for the matched nodes.
	SecretID string `json:"secretID" validate:"required"`
}

// CredentialsSpec is an optional per-resource credential mapping.
type CredentialsSpec struct {
	// Bindings are evaluated in order; the first match wins.
	Bindings []CredentialBinding `json:"bindings,omitempty" validate:"omitempty,dive"`

	// Default is used when no binding matches and no node-named secret exists.
	// Overrides the server-wide default secret.
	Default string `json:"default,omitempty"`
}
```

Matching rules:
- Exact (case-insensitive) node-name match is preferred over glob match.
- Glob matching uses `path.Match` semantics (`*`, `?`, `[...]`).
- Bindings evaluated top-to-bottom; first match wins. Document that ordering matters.
- Reject at validation time: empty `nodes`, empty `secretID`, malformed glob patterns.

### 3.2 FirmwareUpdateJobSpec

- `SecretID` — drop `validate:"required"`, make it `json:"secretID,omitempty"`.
- Add `Credentials *CredentialsSpec `json:"credentials,omitempty" validate:"omitempty"``.

### 3.3 FirmwareUpdateCampaignSpec / FirmwareCampaignTarget

- `FirmwareCampaignTarget.SecretID` — drop `validate:"required"`, add `omitempty`.
- Add `Credentials *CredentialsSpec` to `FirmwareUpdateCampaignSpec` (campaign-level,
  applies to all targets).

### 3.4 Campaign → Job propagation

When the campaign reconciler materializes child jobs it must record the **resolved**
secret ID on the child job's `spec.secretID`. This keeps child jobs self-describing,
makes the resolution auditable, and avoids re-resolving with different inputs.
Alternatively, if resolution must stay late-bound, copy the campaign `credentials`
section down to the child job — pick the "resolve at campaign time" option as the
default because it is simpler to debug; note the decision in the handoff doc.

## 4. Resolution Logic

New file `pkg/reconcilers/credential_resolver.go` (or `pkg/credentials/resolver.go` if
we want it reusable outside reconcilers — prefer `pkg/credentials` so it is unit
testable without reconciler scaffolding).

```go
type ResolveInput struct {
	NodeName      string           // targetAddress
	ExplicitID    string           // spec.secretID / target.secretID
	Credentials   *v1.CredentialsSpec
	ServerDefault string           // from server config
}

type Resolution struct {
	SecretID string
	Source   string // "explicit" | "binding" | "node-name" | "spec-default" | "server-default"
}

func Resolve(ctx context.Context, store SecretLookup, in ResolveInput) (Resolution, error)
```

Order of evaluation:

1. `ExplicitID` non-empty → use it. (Do **not** silently fall through if the secret is
   missing — an explicit reference that does not resolve is a hard error.)
2. Walk `Credentials.Bindings` — exact match pass first, then glob pass. First hit wins.
3. Try `store.Exists(NodeName)` — if a secret is stored under the node name, use it.
4. `Credentials.Default` if set.
5. `ServerDefault` if configured.
6. Error: `no credential could be resolved for node %q (no explicit secretID, no
   matching credentials binding, no secret stored under the node name, no default
   configured)`.

Requirements:
- Add an existence-probe helper to `internal/secretsruntime` (e.g. `Exists(id string) bool`)
  so step 3 does not depend on decrypting and does not log a spurious error.
- **Never log secret values.** Log only the resolved secret ID and the `Source`. Confirm
  `pkg/debug/sanitize.go` covers the new fields.
- Return `HTTPStatusError` with 400 for user-input problems (bad glob, unresolvable
  explicit ID) and 500 only for store failures, matching existing conventions in
  `loadBMCCredentials`.

Refactor `loadBMCCredentials` into `loadBMCCredentialsFor(nodeName, explicitID, creds)`
and update all three call sites.

## 5. Default Credential Configuration

Server-wide default secret ID:

- Flag `--default-secret-id`, env `FIRMWARE_UPDATER_DEFAULT_SECRET_ID`, wired in
  `cmd/server/main.go` alongside `--secrets-file`.
- Empty by default (feature off). If set, validate at startup that the secret exists and
  decodes; log a warning (not a fatal) if it does not, so the server still starts.
- Surface the configured default in the existing startup log block.

`cmd/secret-cli`:
- No schema change needed — the default is just a normal secret ID.
- Add a convenience flag `--set-default` that writes the secret and prints the exact
  server flag/env to use. Optional; keep it if it does not complicate the CLI.

## 6. Backward Compatibility

- Existing jobs/campaigns with `secretID` set continue to work unchanged (step 1).
- Removing `validate:"required"` is a relaxation — no stored resource becomes invalid.
- No storage migration required: specs are stored as `json.RawMessage` in the generic
  `Resource` ent entity, so new optional fields deserialize cleanly and absent fields
  are zero-valued.
- Decide and document whether validation should *warn* when a job has neither
  `secretID`, `credentials`, nor a server default configured — recommend a startup /
  admission warning rather than a rejection, since a node-named secret may be added later.

## 7. Status / Observability

- Add `status.resolvedSecretID` and `status.credentialSource` to job status so operators
  can see which credential was chosen without reading logs.
- Emit an event/log line on resolution failure that names the node and lists which
  resolution steps were attempted.

## 8. Code Generation

API type edits require regeneration — generated files are marked
`Code generated by Fabrica. DO NOT EDIT`:

```bash
fabrica generate
go generate ./...
go build ./...
```

Regenerated artifacts to review in the diff: `cmd/server/models_generated.go`,
`cmd/server/*_handlers_generated.go`, `cmd/server/openapi_generated.go`,
`pkg/client/models_generated.go`, `internal/storage/ent_queries_generated.go`.
Confirm the new optional fields appear in the OpenAPI output and the generated client.

## 9. Testing

Unit tests (`pkg/credentials/resolver_test.go`):
- explicit ID wins over everything
- explicit ID that is missing from the store → error (no fallback)
- exact binding beats glob binding
- binding order decides between two matching globs
- node-name lookup hit and miss
- spec default beats server default
- nothing configured → descriptive error
- malformed glob → validation error
- case-insensitivity of node matching

Integration:
- Campaign with mixed targets: one explicit `secretID`, one covered by a glob binding,
  one resolved by node name, one falling back to the default.
- Verify child jobs record the resolved secret ID.
- Verify no credential material appears in logs or API responses (extend
  `pkg/debug/debug_test.go`).

Regression:
- Re-run the verified curl flows from `planning/HANDOFF-secrets.md` unchanged.

## 10. Documentation Updates

All of the following must be updated as part of this change:

- `README.md` — TL;DR runbook: show that `secretID` is now optional, document the
  node-name convention and `--default-secret-id`.
- `docs/user-guide.md` — new "Credential Resolution" section with the five-step
  precedence chain and a worked example.
- `docs/DeviceProfile.md`, `docs/FirmwareImages.md` — update any example job/campaign
  payloads that show `secretID` as required.
- `planning/secrets.md` — append a note pointing at this plan as the superseding design.
- `planning/SPEC.md` and `planning/UPDATE_API_JOB_CAMPAIGN.md` — update the job and
  campaign spec field tables.
- `cmd/secret-cli/main.go` usage text — mention the node-name convention and the default
  secret.
- Any `--help` / startup output that lists secret-related flags.

## 11. New Handoff Document

Create `planning/HANDOFF-update_credentials.md` following the house format used by
`planning/HANDOFF-secrets.md`:

1. **Implementation Summary** — what changed, the resolution chain, the campaign→job
   propagation decision.
2. **Technical Details** — new API types, new resolver package, new server flag/env,
   files touched, regeneration command.
3. **Verified Testing Evidence** — exact `curl` payloads for each resolution path and
   the observed `status.credentialSource` values.
4. **Usage Notes** — how to store a node-named secret, how to set the default, how to
   write a `credentials` section, how to debug an unresolved credential.
5. **Acceptance Criteria** — checklist mirroring section 12 below.

## 12. Acceptance Criteria

- [ ] `secretID` is optional on both `FirmwareUpdateJob` and campaign targets.
- [ ] Optional `credentials` section accepted on both jobs and campaigns.
- [ ] Node-name lookup works when no explicit ID and no binding match.
- [ ] Server-wide default credential configurable and used as last resort.
- [ ] An explicit but unresolvable `secretID` is a hard error (no silent fallback).
- [ ] Resolution source is visible in job status.
- [ ] No credential values in logs, errors, or API responses.
- [ ] Existing job/campaign payloads continue to work with no edits.
- [ ] Generated code regenerated and committed; OpenAPI reflects new fields.
- [ ] All docs in section 10 updated.
- [ ] `planning/HANDOFF-update_credentials.md` created.

## 13. Suggested Work Order

1. Add `credentials_types.go` and relax `SecretID` validation.
2. Run `fabrica generate` / `go generate ./...`; verify build.
3. Add `Exists` to `internal/secretsruntime`.
4. Implement `pkg/credentials` resolver + unit tests.
5. Wire resolver into the job reconciler (both call sites).
6. Wire resolver into the campaign reconciler; implement resolved-ID propagation.
7. Add `--default-secret-id` flag/env and startup validation.
8. Add status fields and sanitizer coverage.
9. Integration tests.
10. Documentation sweep + handoff doc.
