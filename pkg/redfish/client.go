package redfish

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Error represents a structured Redfish HTTP failure.
type Error struct {
	StatusCode int
	MessageID  string
	Message    string
	Resolution string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	parts := make([]string, 0, 4)
	parts = append(parts, fmt.Sprintf("Redfish returned status %d", e.StatusCode))
	if strings.TrimSpace(e.MessageID) != "" {
		parts = append(parts, fmt.Sprintf("MessageId=%s", strings.TrimSpace(e.MessageID)))
	}
	if strings.TrimSpace(e.Message) != "" {
		parts = append(parts, strings.TrimSpace(e.Message))
	}
	if strings.TrimSpace(e.Resolution) != "" {
		parts = append(parts, fmt.Sprintf("Resolution=%s", strings.TrimSpace(e.Resolution)))
	}

	return strings.Join(parts, ": ")
}

func (e *Error) IsClientError() bool {
	if e == nil {
		return false
	}
	return e.StatusCode >= 400 && e.StatusCode < 500
}

type Client struct {
	targetAddress string
	username      string
	password      string
	httpClient    *http.Client
}

func NewClient(targetAddress, username, password string) *Client {
	return &Client{
		targetAddress: strings.TrimSpace(targetAddress),
		username:      username,
		password:      password,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

func ResolveEndpoint(targetAddress, uri string) string {
	trimmedURI := strings.TrimSpace(uri)
	if strings.HasPrefix(trimmedURI, "http://") || strings.HasPrefix(trimmedURI, "https://") {
		return trimmedURI
	}

	base := strings.TrimSpace(targetAddress)
	if strings.HasPrefix(trimmedURI, "/") {
		return fmt.Sprintf("https://%s%s", base, trimmedURI)
	}
	return fmt.Sprintf("https://%s/%s", base, trimmedURI)
}

func (c *Client) GetJSON(ctx context.Context, uri string) (map[string]interface{}, int, error) {
	return c.doJSON(ctx, http.MethodGet, uri, nil)
}

func (c *Client) PostJSON(ctx context.Context, uri string, payload interface{}) (map[string]interface{}, http.Header, int, error) {
	return c.doJSONWithHeaders(ctx, http.MethodPost, uri, payload)
}

func (c *Client) DiscoverUpdateServiceAction(ctx context.Context) (string, error) {
	body, _, err := c.GetJSON(ctx, "/redfish/v1/UpdateService")
	if err != nil {
		return "", err
	}

	actions, ok := body["Actions"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("auto-discovery failed: no Actions object in UpdateService response")
	}

	var actionTarget string
	if simpleUpdate, ok := actions["#UpdateService.SimpleUpdate"].(map[string]interface{}); ok {
		actionTarget = strings.TrimSpace(asString(simpleUpdate["target"]))
	}
	if actionTarget == "" {
		if simpleUpdate, ok := actions["#SimpleUpdate"].(map[string]interface{}); ok {
			actionTarget = strings.TrimSpace(asString(simpleUpdate["target"]))
		}
	}

	if actionTarget == "" {
		return "", fmt.Errorf("auto-discovery failed: no SimpleUpdate action found in UpdateService")
	}

	return actionTarget, nil
}

func (c *Client) DiscoverTargetsFromInventory(ctx context.Context, component string) ([]string, error) {
	body, _, err := c.GetJSON(ctx, "/redfish/v1/UpdateService/FirmwareInventory")
	if err != nil {
		return nil, err
	}

	members, ok := body["Members"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("auto-discovery failed: no Members array in FirmwareInventory response")
	}

	componentLower := strings.ToLower(strings.TrimSpace(component))
	targets := make([]string, 0)
	for _, member := range members {
		memberMap, ok := member.(map[string]interface{})
		if !ok {
			continue
		}

		memberID := strings.TrimSpace(asString(memberMap["@odata.id"]))
		if memberID == "" {
			continue
		}

		memberDetail, _, err := c.GetJSON(ctx, memberID)
		if err != nil {
			continue
		}

		if containsLower(memberDetail["Id"], componentLower) || containsLower(memberDetail["Name"], componentLower) || containsLower(memberDetail["Description"], componentLower) {
			targets = append(targets, memberID)
		}
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("auto-discovery failed: component %q not found in FirmwareInventory", component)
	}

	return targets, nil
}

func (c *Client) newRequest(ctx context.Context, method, uri string, payload interface{}) (*http.Request, error) {
	var bodyBytes []byte
	if payload != nil {
		marshaled, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal Redfish request body: %w", err)
		}
		bodyBytes = marshaled
	}

	endpoint := ResolveEndpoint(c.targetAddress, uri)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build Redfish %s request: %w", method, err)
	}
	req.SetBasicAuth(c.username, c.password)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}

func (c *Client) doJSON(ctx context.Context, method, uri string, payload interface{}) (map[string]interface{}, int, error) {
	body, _, statusCode, err := c.doJSONWithHeaders(ctx, method, uri, payload)
	return body, statusCode, err
}

func (c *Client) doJSONWithHeaders(ctx context.Context, method, uri string, payload interface{}) (map[string]interface{}, http.Header, int, error) {
	req, err := c.newRequest(ctx, method, uri, payload)
	if err != nil {
		return nil, nil, 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if isLikelyTransientNetworkError(err) {
			return nil, nil, 0, &Error{StatusCode: http.StatusServiceUnavailable, Message: err.Error()}
		}
		return nil, nil, 0, fmt.Errorf("Redfish %s failed: %w", method, err)
	}
	defer resp.Body.Close()

	rawBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("read Redfish response: %w", readErr)
	}

	if resp.StatusCode >= 400 {
		return nil, resp.Header, resp.StatusCode, parseRedfishErrorResponse(resp.StatusCode, resp.Status, rawBody)
	}

	trimmed := strings.TrimSpace(string(rawBody))
	if trimmed == "" {
		return map[string]interface{}{}, resp.Header, resp.StatusCode, nil
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("parse Redfish response: %w", err)
	}

	return body, resp.Header, resp.StatusCode, nil
}

type redfishODataError struct {
	Code                  string               `json:"code"`
	Message               string               `json:"message"`
	MessageExtendedInfo   []redfishMessageInfo `json:"@Message.ExtendedInfo"`
	ExtendedInfo          []redfishMessageInfo `json:"ExtendedInfo"`
	AdditionalMessageInfo []redfishMessageInfo `json:"Messages"`
}

type redfishMessageInfo struct {
	MessageID  string `json:"MessageId"`
	Message    string `json:"Message"`
	Resolution string `json:"Resolution"`
}

type redfishErrorEnvelope struct {
	ODataError redfishODataError `json:"@odata.error"`
}

func parseRedfishErrorResponse(statusCode int, statusText string, body []byte) error {
	parsed := &Error{
		StatusCode: statusCode,
		Message:    fmt.Sprintf("Redfish returned %s", statusText),
	}

	if len(strings.TrimSpace(string(body))) == 0 {
		return parsed
	}

	var envelope redfishErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil {
		odata := envelope.ODataError
		extendedInfo := append([]redfishMessageInfo(nil), odata.MessageExtendedInfo...)
		extendedInfo = append(extendedInfo, odata.ExtendedInfo...)
		extendedInfo = append(extendedInfo, odata.AdditionalMessageInfo...)

		best := selectBestMessageInfo(extendedInfo)
		if best.MessageID != "" {
			parsed.MessageID = best.MessageID
		}
		if best.Message != "" {
			parsed.Message = best.Message
		} else if strings.TrimSpace(odata.Message) != "" {
			parsed.Message = strings.TrimSpace(odata.Message)
		}
		if best.Resolution != "" {
			parsed.Resolution = best.Resolution
		}
		if parsed.MessageID == "" && strings.TrimSpace(odata.Code) != "" {
			parsed.MessageID = strings.TrimSpace(odata.Code)
		}
		return parsed
	}

	var generic map[string]interface{}
	if err := json.Unmarshal(body, &generic); err == nil {
		if msg := strings.TrimSpace(asString(generic["Message"])); msg != "" {
			parsed.Message = msg
		}
		if resolution := strings.TrimSpace(asString(generic["Resolution"])); resolution != "" {
			parsed.Resolution = resolution
		}
		if messageID := strings.TrimSpace(asString(generic["MessageId"])); messageID != "" {
			parsed.MessageID = messageID
		}
	}

	return parsed
}

func selectBestMessageInfo(infos []redfishMessageInfo) redfishMessageInfo {
	var selected redfishMessageInfo
	bestScore := -1
	for _, info := range infos {
		score := scoreMessageInfo(info)
		if score > bestScore {
			selected = info
			bestScore = score
		}
	}
	return selected
}

func scoreMessageInfo(info redfishMessageInfo) int {
	score := 0
	id := strings.ToLower(strings.TrimSpace(info.MessageID))
	msg := strings.ToLower(strings.TrimSpace(info.Message))
	if id != "" {
		score += 2
	}
	if strings.Contains(id, "critical") || strings.Contains(id, "error") || strings.Contains(id, "fail") {
		score += 5
	}
	if strings.Contains(msg, "critical") || strings.Contains(msg, "error") || strings.Contains(msg, "fail") {
		score += 4
	}
	if strings.TrimSpace(info.Resolution) != "" {
		score += 1
	}
	return score
}

func containsLower(value interface{}, tokenLower string) bool {
	if tokenLower == "" {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(asString(value))), tokenLower)
}

func asString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func isLikelyTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}

	if ue, ok := err.(*url.Error); ok {
		err = ue.Err
	}

	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout() || netErr.Temporary()
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "no route to host")
}
