package smd
// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package smd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

func newTestClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: http.DefaultClient}
}

func TestGroupMembers_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/groups/cabinet-x1000" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"label":"cabinet-x1000","members":{"ids":["x0c0s0b0n0","x0c0s1b0n0"]}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	members, err := c.GroupMembers(context.Background(), "cabinet-x1000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 2 || members[0] != "x0c0s0b0n0" {
		t.Fatalf("unexpected members: %v", members)
	}
}

func TestGroupMembers_NotFoundIsTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "No such group", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.GroupMembers(context.Background(), "missing")
	var httpErr *firmwareproxy.HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 HTTPStatusError, got %v", err)
	}
}

func TestGroupMembers_ServerErrorIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.GroupMembers(context.Background(), "grp")
	var httpErr *firmwareproxy.HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 5xx mapped to 503, got %v", err)
	}
}

func TestResolveMemberBMC_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Inventory/ComponentEndpoints/x0c0s0b0n0" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ID":"x0c0s0b0n0","RedfishEndpointID":"x0c0s0b0","RedfishEndpointFQDN":"bmc0.example.com"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	fqdn, bmc, err := c.ResolveMemberBMC(context.Background(), "x0c0s0b0n0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fqdn != "bmc0.example.com" || bmc != "x0c0s0b0" {
		t.Fatalf("unexpected resolution fqdn=%q bmc=%q", fqdn, bmc)
	}
}

func TestResolveMemberBMC_NotFoundIsUnresolvable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _, err := c.ResolveMemberBMC(context.Background(), "x0c0s9b0n0")
	if !errors.Is(err, ErrEndpointNotFound) {
		t.Fatalf("expected ErrEndpointNotFound, got %v", err)
	}
}

func TestResolveMemberBMC_EmptyFQDNIsUnresolvable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ID":"x0c0s0b0n0","RedfishEndpointID":"x0c0s0b0","RedfishEndpointFQDN":""}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _, err := c.ResolveMemberBMC(context.Background(), "x0c0s0b0n0")
	if !errors.Is(err, ErrEndpointNotFound) {
		t.Fatalf("expected ErrEndpointNotFound for empty FQDN, got %v", err)
	}
}
