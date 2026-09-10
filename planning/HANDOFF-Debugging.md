# Debugging and Tracing Implementation Handoff (`FIRMWARE_UPDATER_DEBUG`)

## 1. Summary

Implemented runtime debugging and function tracing functionality controlled by the environment variable `FIRMWARE_UPDATER_DEBUG`.

Key capabilities implemented:
- **Gated Execution**: Debug output is emitted **only** when `FIRMWARE_UPDATER_DEBUG` is set to a truthy value (`true`, `1`, `yes`, `on`).
- **Zero Overhead when Disabled**: `debug.IsEnabled()` performs early short-circuiting to prevent string formatting and allocations when debug mode is off.
- **Function Entry/Exit Tracing**: `debug.Trace()` uses deferred functions to log function entry parameters and execution time on exit across reconcilers, catalog search/load, device profile matching, and CLI commands.
- **Incoming/Outgoing API Call Logging**:
  - Incoming HTTP server requests are logged via `DebugLoggingMiddleware`.
  - Outgoing HTTP calls to Redfish BMCs (`pkg/redfish`), OCI registries (`pkg/firmwareproxy`), and API client operations (`pkg/client`) log method, target endpoint, status code, and duration.
- **Security Data Sanitization**: Built-in sanitization in `pkg/debug/sanitize.go` automatically redacts passwords, tokens, API keys, basic/bearer auth credentials, and sensitive HTTP headers.

---

## 2. Code Changes

1. `pkg/debug/debug.go` (New)
   - Core debug package with `IsEnabled()`, `Trace()`, and `LogAPICall()`.

2. `pkg/debug/sanitize.go` (New)
   - Data sanitization utilities (`SanitizeMap`, `SanitizeSlice`, `SanitizeValue`) to redact passwords, JWT tokens, and authorization credentials.

3. `pkg/debug/debug_test.go` (New)
   - Unit tests for environment detection, map/slice sanitization, and `Trace()` / `LogAPICall()` safety checks.

4. `internal/middleware/debug_middleware.go` (New)
   - HTTP server middleware (`DebugLoggingMiddleware`) for tracing incoming request details, headers, response status code, and duration.

5. `cmd/server/main.go`
   - Bound `FIRMWARE_UPDATER_DEBUG` via `viper.BindEnv("debug", "FIRMWARE_UPDATER_DEBUG")`.
   - Added `DebugLoggingMiddleware` to router middleware chain.
   - Added `debug.Trace` to `runServer`.

6. `cmd/client/main.go`
   - Bound `FIRMWARE_UPDATER_DEBUG` via `viper.BindEnv("debug", "FIRMWARE_UPDATER_DEBUG")`.

7. `pkg/redfish/client.go`
   - Added `debug.Trace` and `debug.LogAPICall` to outgoing Redfish request handling (`doJSONWithHeaders`).

8. `pkg/firmwareproxy/resolver.go`
   - Added `debug.Trace` to payload discovery and resolution functions (`ResolvePayload`, `ResolvePayloadFromDiscovery`, `ResolvePayloadFromInventory`).

9. `pkg/client/client_generated.go`
   - Added `debug.LogAPICall` to `doRequest` for outgoing client HTTP requests.

10. `pkg/reconcilers/firmwareupdatecampaign_reconciler.go` & `firmwareupdatecampaign_reconciler_generated.go`
    - Added `debug.Trace` to `Reconcile` and `reconcileFirmwareUpdateCampaign`.

11. `pkg/reconcilers/firmwareupdatejob_reconciler.go` & `firmwareupdatejob_reconciler_generated.go`
    - Added `debug.Trace` to `Reconcile` and `reconcileFirmwareUpdateJob`.

12. `pkg/firmwareimages/catalog.go`
    - Added `debug.Trace` to `ListFirmwareImages`, `GetFirmwareImage`, and `SearchFirmwareImages`.

13. `pkg/deviceProfiles/matcher.go` & `registry.go`
    - Added `debug.Trace` to `MatchDevice` and `Registry.Register`.

14. `cmd/server/export.go` & `import.go`
    - Added `debug.Trace` to `runExport` and `runImport`.

15. `docs/user-guide.md`
    - Documented `FIRMWARE_UPDATER_DEBUG` usage, truthy values, logging behavior, and data sanitization.

16. `planning/Debugging.md` (New)
    - Created detailed architecture and implementation plan for `FIRMWARE_UPDATER_DEBUG`.

---

## 3. Validation Performed

Executed:
- `go test -v ./pkg/debug`
- `go test ./pkg/firmwareimages ./pkg/firmwareproxy ./pkg/deviceProfiles`

Results:
- `pkg/debug` tests passed completely (100% pass rate).
- `pkg/firmwareimages`, `pkg/firmwareproxy`, and `pkg/deviceProfiles` tests passed without issues.

---

## 4. Operational Usage

Enable debug output before running any `firmware-updater` command or server:

```bash
export FIRMWARE_UPDATER_DEBUG=true
```

Or run directly:

```bash
FIRMWARE_UPDATER_DEBUG=true ./firmware-updater serve
```

Debug outputs are emitted to `os.Stderr` formatted with `[DEBUG] [ENTER]`, `[DEBUG] [EXIT]`, and `[DEBUG] [API]` markers.
