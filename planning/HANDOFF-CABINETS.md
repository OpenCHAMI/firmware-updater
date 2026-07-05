# CABINETS Handoff

## 1. Implementation Summary

`FirmwareUpdateCampaign` now supports a universal cabinet mode in addition to the existing explicit and component-discovery modes.

What changed:

- Campaign validation now accepts `spec.discovery.repository` by itself when both `spec.component` and `spec.ociReference` are omitted.
- The campaign reconciler can crawl Redfish firmware inventory for every target, derive component-specific repository paths from a base repository, compare installed Redfish firmware versions against OCI manifest versions, and create child jobs only for components that actually need an update.
- Universal child jobs bypass their own auto-discovery by receiving the exact resolved `spec.ociReference` and exact Redfish `spec.targets` member URI from the campaign.
- Child jobs are linked with:
  - `campaign-uid`
  - `campaign-target`
  - `campaign-child-key`
- Campaign aggregation now counts actual child jobs, not just input targets, and adds the terminal state `CompletedWithErrors` for mixed success/failure batches.

Docs were updated in [README.md](../README.md) and [docs/user-guide.md](../docs/user-guide.md).

## 2. Exact Verified curl Command

This command was executed successfully against a live local server on `2026-07-05`:

```bash
curl -sS -X POST http://127.0.0.1:18093/firmwareupdatecampaigns/ \
  -H 'Content-Type: application/json' \
  -d '{"metadata":{"name":"auto-cabinet-e2e"},"spec":{"serverProxyAddress":"127.0.0.1","discovery":{"repository":"127.0.0.1:5002/firmware"},"targets":[{"targetAddress":"127.0.0.1:18443","secretID":"cabinet-bmc"}]}}'
```

Observed result:

- Campaign UID: `firmwareupdatecampaign-2730c710`
- Reconciled campaign state: `InProgress`
- Reconciled summary:
  - `total: 1`
  - `pending: 1`
  - `completed: 0`
  - `failed: 0`
- Generated child job:
  - UID: `firmwareupdatejob-2f558849`
  - `spec.ociReference: 127.0.0.1:5002/firmware/bmc:1.1.0`
  - `spec.targets[0]: /redfish/v1/UpdateService/FirmwareInventory/BMC`

The live Redfish mock advertised both `BMC` and `BIOS`. Only the outdated `BMC` component produced a child job; the `BIOS` component was skipped because its installed version was already newer than the repository payload.

## 3. Usage Notes

### 3.1 Campaign modes

Use one of these payload shapes:

- Explicit mode: `spec.ociReference` + `spec.component`
- Component discovery mode: `spec.discovery.repository` + `spec.discovery.hardwareModel` + `spec.discovery.version` + `spec.component`
- Universal cabinet mode: `spec.discovery.repository` only

Universal mode intentionally treats `spec.discovery.repository` as a base path. For a discovered component like `BMC`, the reconciler first checks `.../bmc`, then a compact slug variant if needed, and finally falls back to the base repository path.

### 3.2 Version and inventory behavior

- Firmware inventory is read from `/redfish/v1/UpdateService/FirmwareInventory`.
- Each member detail is inspected for `Id`, `Name`, `Version`, and `RelatedItem` hints.
- Redfish versions are normalized with a semantic-version substring match when possible, so strings like `nc.1.0.0-build1` still compare correctly against OCI annotations like `1.1.0`.
- If no newer compatible artifact exists for a discovered component, no child job is created for that component.

### 3.3 Child job linkage and aggregation

- `campaign-uid` links all child jobs back to the parent campaign.
- `campaign-target` preserves the original BMC address.
- `campaign-child-key` disambiguates multiple component-specific child jobs for the same BMC.

Campaign state rules now are:

- `Pending`: no child jobs exist yet.
- `InProgress`: at least one child job is still pending or active.
- `Completed`: every child job completed successfully.
- `Failed`: every child job failed.
- `CompletedWithErrors`: all child jobs finished, with a mix of success and failure.

### 3.4 Validation rules

Campaign creation fails when:

- `spec.targets` is empty
- any target is missing `targetAddress`
- any target is missing `secretID`
- both `spec.ociReference` and `spec.discovery` are set
- neither `spec.ociReference` nor `spec.discovery` is set
- `spec.ociReference` is set without `spec.component`
- `spec.discovery.hardwareModel` or `spec.discovery.version` is omitted in component-discovery mode

### 3.5 Verification environment

The verified run used:

- local Fabrica server on port `18093`
- local OCI registry on port `5002`
- local HTTPS Redfish mock on port `18443`
- encrypted secret store generated with `cmd/secret-cli`
- SQLite database at a temporary filesystem path

Docker was not required for the verified run because a local `registry` binary was available in the environment.