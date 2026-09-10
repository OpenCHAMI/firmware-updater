# Debugging and Tracing Plan (`FIRMWARE_UPDATER_DEBUG`)

## 1. Overview & Intent

This plan defines the architecture, instrumentation strategy, and implementation steps to output comprehensive runtime debug information across `firmware-updater`.

### Key Objectives
1. **API Call Logging**: Log detailed request/response metadata whenever incoming or outgoing APIs are invoked.
2. **Function Entry/Exit Tracing**: Trace function invocation lifecycle (entry, execution time, exit state) across core packages and reconcilers.
3. **Gated Execution**: Debug log output **must only** be emitted when `FIRMWARE_UPDATER_DEBUG` is enabled (`true`, `1`, `yes`, `on`).
4. **Zero Overhead when Disabled**: Short-circuit trace logic early to avoid string formatting, allocations, or timing overhead when debugging is disabled.
5. **Security & Data Sanitization**: Redact sensitive data (JWTs, passwords, auth headers, secret payload values) in all debug outputs.

---

## 2. Environment Variable & Configuration Strategy

### 2.1 Configuration Resolution
`FIRMWARE_UPDATER_DEBUG` will serve as the unified environment variable controlling debug output across all binary entrypoints (`cmd/server`, `cmd/client`, `cmd/secret-cli`).

```
Environment Variable: FIRMWARE_UPDATER_DEBUG (values: "true", "1", "TRUE", "yes")
CLI Flag (Persistent):  --debug
```

### 2.2 Integration Rules
1. **Viper & Cobra**: Ensure `viper.BindEnv("debug", "FIRMWARE_UPDATER_DEBUG")` is registered in `cmd/server/main.go` and `cmd/client/main.go`.
2. **Direct Environment Fallback**: In `pkg/` packages where Viper config is not directly injected, query `os.Getenv("FIRMWARE_UPDATER_DEBUG")` via a centralized helper module.
3. **Runtime Dynamic Toggle**: Allow `FIRMWARE_UPDATER_DEBUG` to be queried dynamically so logger behavior adapts immediately without requiring process restart where applicable.

---

## 3. Architecture: Core Debug Package (`pkg/debug`)

A new package `pkg/debug` will encapsulate all tracing and debug utilities.

### 3.1 Core API Specification (`pkg/debug/debug.go`)

```go
package debug

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// IsEnabled returns true if FIRMWARE_UPDATER_DEBUG is set to a truthy value.
func IsEnabled() bool {
	v := os.Getenv("FIRMWARE_UPDATER_DEBUG")
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}

// Trace logs function entry and defers exit logging with execution duration.
// Usage: defer debug.Trace("PackageName.FunctionName", "param1", val1)()
func Trace(funcName string, args ...any) func() {
	if !IsEnabled() {
		return func() {}
	}

	start := time.Now()
	entryMsg := fmt.Sprintf("[DEBUG] [ENTER] %s", funcName)
	if len(args) > 0 {
		entryMsg += fmt.Sprintf(" | args: %v", args)
	}
	fmt.Fprintln(os.Stderr, entryMsg)

	return func() {
		elapsed := time.Since(start)
		fmt.Fprintf(os.Stderr, "[DEBUG] [EXIT]  %s | duration: %s\n", funcName, elapsed)
	}
}

// LogAPICall outputs structured debug info for API invocations.
func LogAPICall(direction, category, method, endpoint string, statusCode int, duration time.Duration, details map[string]any) {
	if !IsEnabled() {
		return
	}

	sanitizedDetails := sanitize(details)
	fmt.Fprintf(os.Stderr, "[DEBUG] [API] [%s] [%s] %s %s -> Status: %d | duration: %s | details: %v\n",
		direction, category, method, endpoint, statusCode, duration, sanitizedDetails)
}
```

---

## 4. API Call Instrumentation Strategy

API tracing is divided into **Incoming Server APIs** and **Outgoing Client/External APIs**.

### 4.1 Incoming Server HTTP APIs (`internal/middleware`)
Create HTTP server middleware (`internal/middleware/debug_middleware.go`):

- **Target**: Intercept all incoming API calls in `cmd/server/main.go`.
- **Logged Metadata**:
  - HTTP Method & Request URI
  - Remote IP
  - Selected Request Headers (Sanitized: `Authorization`, `Cookie` masked)
  - Response Status Code & Content Length
  - Total Request Processing Duration
- **Condition**: Executed conditionally if `debug.IsEnabled()`.

```go
func DebugLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !debug.IsEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		
		debug.LogAPICall("INCOMING", "HTTP", r.Method, r.URL.Path, 0, 0, map[string]any{
			"remote_addr": r.RemoteAddr,
			"user_agent":  r.UserAgent(),
		})

		next.ServeHTTP(rw, r)

		debug.LogAPICall("INCOMING", "HTTP", r.Method, r.URL.Path, rw.statusCode, time.Since(start), nil)
	})
}
```

### 4.2 Outgoing API Calls

#### A. Redfish BMC API Calls (`pkg/redfish/client.go`)
- Instrument Redfish HTTP Client (`DoRequest` / `SimpleUpdate`).
- Log endpoint URI, HTTP action, HTTP status code, attempt duration, and Redfish task location header.

#### B. OCI / Quay Registry API Calls (`pkg/firmwareproxy/resolver.go`)
- Instrument authentication and manifest/blob fetch operations.
- Log registry host, image tag/digest, request method, status code, and stream duration.

#### C. Generated API Client (`pkg/client/client_generated.go`)
- Leverage existing zerolog debug hooks in `pkg/client/client_generated.go` and bridge them with `FIRMWARE_UPDATER_DEBUG`.

---

## 5. Function Entry/Exit Tracing Plan

Function entry/exit tracing will be placed in primary execution paths.

### 5.1 Target Packages & Key Functions

| Package | File | Target Functions |
| :--- | :--- | :--- |
| `pkg/reconcilers` | `firmwareupdatecampaign_reconciler.go` | `Reconcile`, `processJobs`, `updateCampaignStatus` |
| `pkg/reconcilers` | `firmwareupdatejob_reconciler.go` | `Reconcile`, `executeRedfishUpdate`, `checkJobStatus` |
| `pkg/firmwareproxy` | `resolver.go` | `ResolveImageURL`, `AuthenticateRegistry`, `GetImageManifest` |
| `pkg/firmwareimages` | `catalog.go`, `store.go` | `LoadCatalog`, `ValidateImage`, `FindMatchingImage` |
| `pkg/deviceProfiles` | `matcher.go`, `registry.go` | `MatchDeviceProfile`, `LoadProfilesFromDir` |
| `cmd/server` | `main.go`, `export.go`, `import.go` | `runServer`, `runExport`, `runImport` |

### 5.2 Instrumentation Pattern Example
```go
func (r *FirmwareUpdateJobReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if debug.IsEnabled() {
		defer debug.Trace("FirmwareUpdateJobReconciler.Reconcile", "job", req.Name, "namespace", req.Namespace)()
	}

	// Reconciler implementation logic...
}
```

---

## 6. Implementation Phases & Task Checklist

### Phase 1: Core Infrastructure
- [ ] Create `pkg/debug/debug.go` with `IsEnabled()`, `Trace()`, and `LogAPICall()`.
- [ ] Implement data sanitization helper in `pkg/debug/sanitize.go` to mask passwords, bearer tokens, and credentials.
- [ ] Add unit tests for `pkg/debug` in `pkg/debug/debug_test.go`.

### Phase 2: Server & Middleware Integration
- [ ] Register `FIRMWARE_UPDATER_DEBUG` environment variable in `cmd/server/main.go` and `cmd/client/main.go`.
- [ ] Create `internal/middleware/debug_middleware.go`.
- [ ] Mount debug middleware to HTTP router in `cmd/server/main.go`.

### Phase 3: Outgoing API Instrumentation
- [ ] Add API debug logging to `pkg/redfish/client.go`.
- [ ] Add API debug logging to `pkg/firmwareproxy/resolver.go`.
- [ ] Ensure `pkg/client/client_generated.go` respects `FIRMWARE_UPDATER_DEBUG`.

### Phase 4: Function Entry/Exit Tracing
- [ ] Instrument `pkg/reconcilers/firmwareupdatecampaign_reconciler.go` and `firmwareupdatejob_reconciler.go`.
- [ ] Instrument `pkg/firmwareproxy/resolver.go`.
- [ ] Instrument `pkg/firmwareimages/catalog.go` and `store.go`.
- [ ] Instrument `pkg/deviceProfiles/matcher.go`.

### Phase 5: Verification & Documentation
- [ ] Run test suite with `FIRMWARE_UPDATER_DEBUG=true` to verify debug outputs.
- [ ] Run test suite with `FIRMWARE_UPDATER_DEBUG=false` to verify zero log pollution.
- [ ] Update `docs/user-guide.md` with instructions on enabling `FIRMWARE_UPDATER_DEBUG`.

---

## 7. Security & Performance Verification

1. **Security**: Ensure no sensitive tokens, secret store keys, or credentials appear in debug logs.
2. **Performance**: Verify `if !debug.IsEnabled()` guards prevent unnecessary `fmt.Sprintf` parameter evaluations.
