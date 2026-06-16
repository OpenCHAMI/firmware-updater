# Phase 2: Reconciliation Implementation - Universal Redfish Dispatcher

## 1. Context Acquisition

Read the `apis/example.fabrica.dev/v1/firmwareupdatejob_types.go` file to understand the current `Spec` schema. The objective is to expose the Redfish HTTP action path as a configurable parameter to support non-standard BMCs, Chassis Controllers, and Cabinet Controllers. Do not modify the underlying database driver or storage types.

## 2. Reconciliation State Machine

Implement the following logic inside the generated Fabrica reconciler loop located in `pkg/reconcilers/firmwareupdatejob_reconciler.go`.

* **Pre-flight Checks:**
* Evaluate the user-provided `Spec.UpdateURI`. If the field is empty, default the string to the standard DMTF path: `/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate`.
* Validate that if an `UpdateURI` is explicitly provided, it begins with `/redfish/v1/`.


* **Execution Steps:**
* Step 1: In the Redfish Dispatch phase, modify the HTTP POST request builder to route the payload to the dynamically resolved `UpdateURI` rather than the hardcoded string.
* Step 2: Ensure the Redfish JSON payload (`ImageURI`, `Targets`, `TransferProtocol`) remains intact and is sent to the new dynamic path.


* **Error Handling:**
* A malformed `UpdateURI` (e.g., failing the `/redfish/v1/` prefix check) is a terminal error. Halt execution before attempting to dial the OCI registry or the hardware.
* Network timeouts to the new controller IPs remain transient errors governed by the existing exponential backoff strategy.



## 3. State Updates

Based on the execution steps, update the resource's `Status` field explicitly.

* **On Success:** Transition `Status.JobState` to `InProgress` and inject the external Redfish Task ID if the chassis controller returns one.
* **On Transient Failure:** Keep `Status.JobState` as `Resolving` or `Pending` depending on the phase, allowing the reconciler to retry the connection to the controller.
* **On Terminal Failure (Malformed URI):** Set `Status.JobState` to `Failed` and append the exact error message: "invalid UpdateURI: must begin with /redfish/v1/".

## 4. Acceptance Criteria

* **Code Generation:** The agent must add the `UpdateURI string` field (with `omitempty` JSON tags) to the Spec struct and execute `fabrica generate` to rebuild the models.
* **Compilation:** The code must compile. Run `go mod tidy` and `go build ./...`.
* **Idempotency Verification:** The reconciler must be idempotent. It should be able to run multiple times against the same `Spec` without duplicating external Redfish calls if the state is already `InProgress`.
* **Testing:** Stage dummy payloads in the local registry. Execute the following commands to verify the `UpdateURI` parameter successfully routes the `Targets` to the correct hardware paths:

**Test 1: Node BIOS Update**

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ -H 'Content-Type: application/json' -d '{"metadata":{"name":"node1-bios-update"},"spec":{"targetAddress":"10.104.0.40","username":"root","password":"initial0","ociReference":"127.0.0.1:5000/firmware/bios:1.8.2","targets":["/redfish/v1/UpdateService/FirmwareInventory/Node1.BIOS"],"serverProxyAddress":"10.254.1.20","updateURI":"/redfish/v1/UpdateService/Actions/SimpleUpdate"}}'

```

**Test 2: Cabinet Controller Update**

```bash
curl -sS -X POST http://127.0.0.1:8090/firmwareupdatejobs/ -H 'Content-Type: application/json' -d '{"metadata":{"name":"cabinet-controller-update"},"spec":{"targetAddress":"10.104.0.35","username":"root","password":"initial0","ociReference":"127.0.0.1:5000/firmware/cc:1.9.6","targets":["/redfish/v1/UpdateService/FirmwareInventory/BMC"],"serverProxyAddress":"10.254.1.20","updateURI":"/redfish/v1/UpdateService/Actions/SimpleUpdate"}}'

```

## 5. Output Artifacts

Generate a `CHASSIS_HANDOFF.md` containing:

1. A brief summary of the implemented logic.
3. The exact, verified `curl` command that successfully tested the code.
4. Detailed notes on important details for using the code, whereby someone with no context could fully utilize the code you wrote and the endpoints as expected and fully understand the implementation.