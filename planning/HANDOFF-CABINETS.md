# CABINETS Handoff

## 1. Implementation Summary

`FirmwareUpdateCampaign` is now a first-class resource for bulk cabinet updates. A campaign carries the shared payload settings once, plus a `targets` array containing each BMC target and its `secretID`.

When a campaign is created, the reconciler spawns one `FirmwareUpdateJob` per target, assigns each child job a stable UID before persistence, and annotates the child with:

- `campaign-uid`: the parent campaign UID
- `campaign-target`: the target address for that child job

The campaign status is aggregated from those child jobs and reports:

- `campaignState`
- `summary.total`
- `summary.completed`
- `summary.failed`
- `summary.pending`
- `childJobs[]` with per-target job UID and job state

The user-facing docs were updated in both [README.md](../README.md) and [docs/user-guide.md](../docs/user-guide.md).

## 2. Exact Verified curl Command

This command was executed successfully against the live server and returned a created campaign with two child jobs linked to it:

```bash
curl -sS -X POST http://127.0.0.1:18091/firmwareupdatecampaigns/ \
  -H 'Content-Type: application/json' \
  -d '{
    "metadata": {
      "name": "cabinet-demo"
    },
    "spec": {
      "serverProxyAddress": "127.0.0.1",
      "component": "BMC",
      "ociReference": "127.0.0.1:5000/firmware/test-bmc:1.0.0",
      "targets": [
        {
          "targetAddress": "127.0.0.1",
          "secretID": "campaign-bmc"
        },
        {
          "targetAddress": "192.0.2.11",
          "secretID": "campaign-bmc"
        }
      ]
    }
  }'
```

Observed result:

- Campaign UID: `firmwareupdatecampaign-07e9ef0d`
- Child job UIDs:
  - `firmwareupdatejob-2507543a`
  - `firmwareupdatejob-c3694556`
- Campaign status after reconciliation:
  - `campaignState: InProgress`
  - `summary.total: 2`
  - `summary.pending: 2`

## 3. Usage Notes

### 3.1 Campaign payload shape

Use `POST /firmwareupdatecampaigns/` with:

- `metadata.name` for a human-readable campaign name
- `spec.serverProxyAddress` for the routable proxy address
- exactly one of `spec.ociReference` or `spec.discovery`
- `spec.component` when the child jobs should auto-discover the Redfish update target by component name
- `spec.targets[]` with one object per BMC

Each entry in `spec.targets` must include:

- `targetAddress`
- `secretID`

### 3.2 How child jobs are linked

Child jobs are annotated during reconciliation so the parent campaign can re-find them later:

- `campaign-uid` lets the campaign status aggregator query the correct children
- `campaign-target` preserves the target address even if the child job name changes

The child job name is derived from the campaign name and target address, but the annotation is the actual linkage used for aggregation.

### 3.3 Status behavior

The campaign status is not a static echo of the request. Reconciliation updates it based on the child jobs it can observe in storage.

Expected states:

- `Pending` before reconciliation starts
- `InProgress` while children are still pending or running
- `Completed` when every child job is completed
- `Failed` when every remaining child job is terminally failed and no child remains active

### 3.4 Validation rules

Campaign creation will fail if:

- `spec.targets` is empty
- a target is missing `targetAddress`
- a target is missing `secretID`
- both `spec.ociReference` and `spec.discovery` are set
- neither `spec.ociReference` nor `spec.discovery` is set

### 3.5 Local runtime requirements used during verification

The server requires a valid `MASTER_KEY` and an encrypted secrets file at startup. For the verified run, a temporary encrypted store was generated with `cmd/secret-cli`, then the server was started with:

```bash
MASTER_KEY=<64-char-hex> ./server serve --secrets-file <path-to-secrets.json>
```

The verification run also used a local SQLite database and a non-default test port because `18090` was already in use in the workspace.