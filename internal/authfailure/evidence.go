// Package authfailure classifies credential rejection independently of display text.
package authfailure

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Source string

const (
	UpstreamQuota    Source = "upstream_quota"
	ControlPlane     Source = "control_plane"
	Transport        Source = "transport"
	TokenRefresh     Source = "token_refresh"
	Inference        Source = "inference"
	Supplemental     Source = "supplemental"
	CredentialStatus Source = "credential_status"
)

// Evidence is transient: provider messages can contain secrets and must never be
// serialized, logged or persisted. Error exposes only a safe status summary.
type Evidence struct {
	Source      Source
	Endpoint    string
	HTTPStatus  int
	Code        string
	Type        string
	Message     string
	Description string
	Reason      string
	HasEnvelope bool
}

// MarshalJSON prevents accidental raw evidence disclosure outside classification.
func (e *Evidence) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

func (e *Evidence) Error() string {
	if e.Source == ControlPlane {
		return fmt.Sprintf("CPA returned HTTP %d", e.HTTPStatus)
	}
	if e.HTTPStatus >= 400 {
		return fmt.Sprintf("HTTP %d", e.HTTPStatus)
	}
	return "invalid upstream response"
}

func Parse(source Source, endpoint string, status int, body []byte) *Evidence {
	e := &Evidence{Source: source, Endpoint: endpoint, HTTPStatus: status}
	var object map[string]any
	if json.Unmarshal(body, &object) != nil {
		e.Message = string(body)
		return e
	}
	var walk func(map[string]any)
	walk = func(o map[string]any) {
		for key, target := range map[string]*string{"code": &e.Code, "type": &e.Type, "message": &e.Message, "error_description": &e.Description, "reason": &e.Reason} {
			if value, ok := o[key].(string); ok && value != "" {
				*target = value
			}
		}
		if code, ok := o["code"].(float64); ok {
			e.Code = fmt.Sprint(code)
		}
		if value, ok := o["status"].(string); ok && e.Code == "" {
			e.Code = value
		}
		if value, ok := o["error"]; ok && value != nil {
			e.HasEnvelope = true
			switch v := value.(type) {
			case string:
				e.Code = v
			case map[string]any:
				walk(v)
			}
		}
		for _, key := range []string{"errors", "details"} {
			if entries, ok := o[key].([]any); ok {
				for _, entry := range entries {
					if item, ok := entry.(map[string]any); ok {
						walk(item)
					}
				}
			}
		}
	}
	walk(object)
	// Connect-RPC errors carry a top-level code and message.
	if e.Code != "" && (e.Message != "" || e.Description != "") {
		e.HasEnvelope = true
	}
	return e
}

func (e *Evidence) NeedsReauth() bool {
	if e == nil || e.Source == ControlPlane || e.Source == Transport || e.Source == Supplemental {
		return false
	}
	if e.HTTPStatus == 429 || e.HTTPStatus >= 500 {
		return false
	}
	structured := strings.ToLower(strings.Join([]string{e.Code, e.Type, e.Reason}, " "))
	text := strings.ToLower(strings.Join([]string{e.Code, e.Type, e.Message, e.Description, e.Reason}, " "))
	// Provider identifiers carry deliberate semantics. Endpoint vocabulary in
	// prose cannot veto an explicit token/session rejection, while structured
	// client and permission failures still do not ask the user to log in again.
	if containsAny(structured, "invalid_client", "unauthorized_client") {
		return false
	}
	if strings.Contains(structured, "invalid_grant") {
		return e.Source == TokenRefresh
	}
	if e.HTTPStatus == 401 {
		if containsAny(text, "invalid_client", "unauthorized_client") {
			return false
		}
		if strings.Contains(text, "invalid_grant") {
			return e.Source == TokenRefresh
		}
		return true
	}
	if containsAny(structured, "payment_required", "quota", "entitlement", "policy", "scope", "risk", "challenge") {
		return false
	}
	if containsAny(structured, "invalid_token", "token_expired", "invalid_session", "session_expired", "session_revoked", "authentication_error", "unauthenticated", "invalid_api_key") {
		return true
	}
	if containsAny(text, "invalid_client", "unauthorized_client") {
		return false
	}
	if strings.Contains(text, "invalid_grant") {
		return e.Source == TokenRefresh
	}
	if containsAny(text, "payment_required", "quota", "entitlement", "policy", "scope", "risk", "challenge") {
		return false
	}
	return containsAny(text, "invalid_token", "invalid token", "token expired", "token has expired", "token is expired", "credential expired", "session expired", "session invalid", "session has expired", "re-authenticate", "reauthenticate", "authentication_error", "unauthenticated", "invalid_api_key", "invalid api key", "authentication failed", "auth failed")
}
func containsAny(text string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

var HTTP_STATUS = regexp.MustCompile(`(?i)\b(?:HTTP\s+)?(401|403|429|5[0-9]{2})\b`)

// FromText is limited to legacy credential status messages with no typed origin.
// Control-plane and transport vocabulary always prevents account attribution.
func FromText(text string) *Evidence {
	e := &Evidence{Source: CredentialStatus, Message: text}
	lower := strings.ToLower(text)
	if containsAny(lower, "refresh token", "refresh_token", "token refresh") {
		e.Source = TokenRefresh
	}
	if containsAny(lower, "cpa returned", "management key", "management_key", "proxy permission") {
		e.Source = ControlPlane
	}
	if containsAny(lower, "timeout", "timed out", "deadline exceeded", "dial tcp", "connection refused", "network") {
		e.Source = Transport
	}
	if match := HTTP_STATUS.FindStringSubmatch(text); len(match) > 1 {
		fmt.Sscanf(match[1], "%d", &e.HTTPStatus)
	}
	return e
}
