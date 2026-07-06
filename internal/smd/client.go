package smd
// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package smd provides a minimal client for the OpenCHAMI State Management
// Database (SMD) HSM v2 API. firmware-updater consumes existing groups and
// component-endpoint inventory only; it never creates or mutates SMD state.
//
// Contract verified against OpenCHAMI/smd (master). Base path defaults to
// "/hsm/v2" on the SMD host and is configurable via the SMD_BASE_URL env var.
package smd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

// ErrEndpointNotFound signals that a member xname has no ComponentEndpoint in
// SMD (e.g. not yet discovered). Callers treat this as "unresolvable", not as a
// hard error, so that AllowPartialTargets can be honored.
var ErrEndpointNotFound = errors.New("smd: component endpoint not found")

// DefaultBaseURL is used when SMD_BASE_URL is not set.
const DefaultBaseURL = "http://smd:27779/hsm/v2"

// Client is a read-only SMD HSM v2 client.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClientFromEnv builds a Client from environment configuration.
//
//   - SMD_BASE_URL: full base path to the SMD HSM v2 API (default
//     "http://smd:27779/hsm/v2"). SMD deployments are typically fronted by a
//     JWT/gateway; SMD handlers do not enforce authz themselves.
//   - SMD_TOKEN: optional bearer token attached to outgoing requests.
func NewClientFromEnv() *Client {
	base := strings.TrimSpace(os.Getenv("SMD_BASE_URL"))
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(base, "/"),
		token:   strings.TrimSpace(os.Getenv("SMD_TOKEN")),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Group mirrors the fields of sm.Group that firmware-updater consumes.
type Group struct {
	Label   string `json:"label"`
	Members struct {
		IDs []string `json:"ids"`
	} `json:"members"`
}

// ComponentEndpoint mirrors the fields of sm.ComponentEndpoint that
// firmware-updater consumes.
type ComponentEndpoint struct {
	ID                  string `json:"ID"`
	RedfishEndpointID   string `json:"RedfishEndpointID"`
	RedfishEndpointFQDN string `json:"RedfishEndpointFQDN"`
}

// GroupMembers fetches the member xnames of an SMD user-defined group.
//
// A 404 ("No such group") is returned as an *firmwareproxy.HTTPStatusError with
// StatusCode 404 so callers can surface it as a terminal error. Transport
// failures and 5xx responses are returned as transient (503) errors.
func (c *Client) GroupMembers(ctx context.Context, groupRef string) ([]string, error) {
	endpoint := fmt.Sprintf("%s/groups/%s", c.baseURL, url.PathEscape(groupRef))

	var group Group
	if err := c.getJSON(ctx, endpoint, &group); err != nil {
		return nil, err
	}
	return group.Members.IDs, nil
}

// ResolveMemberBMC resolves a member xname to its controlling BMC's Redfish
// FQDN via the ComponentEndpoints inventory. SMD inventory is authoritative;
// xname string manipulation is intentionally not used.
//
// Returns ErrEndpointNotFound when the member has no ComponentEndpoint (404) or
// when the endpoint carries no Redfish FQDN, so the caller can treat the member
// as unresolvable.
func (c *Client) ResolveMemberBMC(ctx context.Context, xname string) (fqdn string, bmcXname string, err error) {
	endpoint := fmt.Sprintf("%s/Inventory/ComponentEndpoints/%s", c.baseURL, url.PathEscape(xname))

	var ep ComponentEndpoint
	if err := c.getJSON(ctx, endpoint, &ep); err != nil {
		var httpErr *firmwareproxy.HTTPStatusError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return "", "", ErrEndpointNotFound
		}
		return "", "", err
	}

	if strings.TrimSpace(ep.RedfishEndpointFQDN) == "" {
		return "", "", ErrEndpointNotFound
	}
	return ep.RedfishEndpointFQDN, ep.RedfishEndpointID, nil
}

// getJSON performs a GET and decodes a JSON body, mapping HTTP/transport
// failures onto *firmwareproxy.HTTPStatusError so the reconciler's existing
// terminal/transient classification applies uniformly.
func (c *Client) getJSON(ctx context.Context, endpoint string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build SMD request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Treat transport failures as transient.
		return &firmwareproxy.HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		msg := fmt.Sprintf("SMD GET %s returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
		// 5xx are transient; 4xx are terminal.
		code := resp.StatusCode
		if code >= 500 {
			code = http.StatusServiceUnavailable
		}
		return &firmwareproxy.HTTPStatusError{StatusCode: code, Message: msg}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &firmwareproxy.HTTPStatusError{StatusCode: http.StatusBadGateway, Message: fmt.Sprintf("decode SMD response: %v", err)}
	}
	return nil
}
