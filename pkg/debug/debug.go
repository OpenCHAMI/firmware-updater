package debug

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// IsEnabled returns true if FIRMWARE_UPDATER_DEBUG is set to a truthy value ("true", "1", "yes", "on").
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
		sanitizedArgs := SanitizeSlice(args)
		entryMsg += fmt.Sprintf(" | args: %v", sanitizedArgs)
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

	sanitizedDetails := SanitizeMap(details)
	if statusCode > 0 {
		fmt.Fprintf(os.Stderr, "[DEBUG] [API] [%s] [%s] %s %s -> Status: %d | duration: %s | details: %v\n",
			direction, category, method, endpoint, statusCode, duration, sanitizedDetails)
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] [API] [%s] [%s] %s %s | details: %v\n",
			direction, category, method, endpoint, sanitizedDetails)
	}
}
