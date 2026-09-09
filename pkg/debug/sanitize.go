package debug

import (
	"fmt"
	"strings"
)

var sensitiveKeys = []string{
	"password",
	"passwd",
	"pass",
	"secret",
	"token",
	"jwt",
	"auth",
	"authorization",
	"cookie",
	"bearer",
	"api_key",
	"apikey",
}

// isSensitiveKey checks if a key string contains any sensitive keywords.
func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, sk := range sensitiveKeys {
		if strings.Contains(k, sk) {
			return true
		}
	}
	return false
}

// SanitizeMap creates a copy of the map with sensitive key values redacted.
func SanitizeMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		if isSensitiveKey(k) {
			result[k] = "[REDACTED]"
		} else {
			result[k] = SanitizeValue(v)
		}
	}
	return result
}

// SanitizeSlice sanitizes each element in a slice.
func SanitizeSlice(slice []any) []any {
	if slice == nil {
		return nil
	}
	result := make([]any, len(slice))
	for i, v := range slice {
		result[i] = SanitizeValue(v)
	}
	return result
}

// SanitizeValue handles nested maps, slices, or string checks.
func SanitizeValue(val any) any {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case map[string]any:
		return SanitizeMap(v)
	case []any:
		return SanitizeSlice(v)
	case string:
		// Basic inline token check if value looks like "Bearer xxx" or starts with "Basic "
		lower := strings.ToLower(v)
		if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") {
			return "[REDACTED]"
		}
		return v
	case fmt.Stringer:
		str := v.String()
		lower := strings.ToLower(str)
		if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") {
			return "[REDACTED]"
		}
		return str
	default:
		return v
	}
}
