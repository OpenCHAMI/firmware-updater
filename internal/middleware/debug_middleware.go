/*
 * Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
 *
 * SPDX-License-Identifier: MIT
 */

package server

import (
	"net/http"
	"time"

	"github.com/user/firmware-updater/pkg/debug"
)

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *statusResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *statusResponseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// DebugLoggingMiddleware logs incoming HTTP requests when FIRMWARE_UPDATER_DEBUG is enabled.
func DebugLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !debug.IsEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		headers := make(map[string]any)
		if auth := r.Header.Get("Authorization"); auth != "" {
			headers["Authorization"] = auth
		}
		if cookie := r.Header.Get("Cookie"); cookie != "" {
			headers["Cookie"] = cookie
		}

		debug.LogAPICall("INCOMING", "HTTP", r.Method, r.URL.Path, 0, 0, map[string]any{
			"remote_addr": r.RemoteAddr,
			"user_agent":  r.UserAgent(),
			"query":       r.URL.RawQuery,
			"headers":     headers,
		})

		next.ServeHTTP(rw, r)

		debug.LogAPICall("INCOMING", "HTTP", r.Method, r.URL.Path, rw.statusCode, time.Since(start), map[string]any{
			"bytes_written": rw.bytesWritten,
		})
	})
}
