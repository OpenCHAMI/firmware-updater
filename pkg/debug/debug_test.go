package debug

import (
	"os"
	"testing"
	"time"
)

func TestIsEnabled(t *testing.T) {
	tests := []struct {
		envVal   string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"", false},
		{"random", false},
	}

	origEnv := os.Getenv("FIRMWARE_UPDATER_DEBUG")
	defer os.Setenv("FIRMWARE_UPDATER_DEBUG", origEnv)

	for _, tt := range tests {
		os.Setenv("FIRMWARE_UPDATER_DEBUG", tt.envVal)
		got := IsEnabled()
		if got != tt.expected {
			t.Errorf("IsEnabled() with env %q = %v; want %v", tt.envVal, got, tt.expected)
		}
	}
}

func TestSanitizeMap(t *testing.T) {
	input := map[string]any{
		"user":          "admin",
		"password":      "secret123",
		"Authorization": "Bearer tokenXYZ",
		"nested": map[string]any{
			"api_key": "key12345",
			"normal":  "value",
		},
	}

	sanitized := SanitizeMap(input)

	if sanitized["user"] != "admin" {
		t.Errorf("expected user to be admin, got %v", sanitized["user"])
	}
	if sanitized["password"] != "[REDACTED]" {
		t.Errorf("expected password to be [REDACTED], got %v", sanitized["password"])
	}
	if sanitized["Authorization"] != "[REDACTED]" {
		t.Errorf("expected Authorization to be [REDACTED], got %v", sanitized["Authorization"])
	}

	nested, ok := sanitized["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map type")
	}
	if nested["api_key"] != "[REDACTED]" {
		t.Errorf("expected api_key to be [REDACTED], got %v", nested["api_key"])
	}
	if nested["normal"] != "value" {
		t.Errorf("expected normal to be value, got %v", nested["normal"])
	}
}

func TestTraceAndLogAPICallDisabled(t *testing.T) {
	origEnv := os.Getenv("FIRMWARE_UPDATER_DEBUG")
	defer os.Setenv("FIRMWARE_UPDATER_DEBUG", origEnv)
	os.Setenv("FIRMWARE_UPDATER_DEBUG", "false")

	// Should do nothing and not panic
	done := Trace("TestFunc", "arg1")
	done()

	LogAPICall("INCOMING", "HTTP", "GET", "/api/v1/test", 200, 10*time.Millisecond, map[string]any{"key": "val"})
}

func TestTraceAndLogAPICallEnabled(t *testing.T) {
	origEnv := os.Getenv("FIRMWARE_UPDATER_DEBUG")
	defer os.Setenv("FIRMWARE_UPDATER_DEBUG", origEnv)
	os.Setenv("FIRMWARE_UPDATER_DEBUG", "true")

	// Should execute trace defer and api log without panicking
	done := Trace("TestFunc", "arg1", map[string]any{"password": "123"})
	done()

	LogAPICall("INCOMING", "HTTP", "GET", "/api/v1/test", 200, 10*time.Millisecond, map[string]any{"user": "test"})
}
