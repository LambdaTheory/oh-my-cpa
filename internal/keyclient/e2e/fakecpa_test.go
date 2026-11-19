//go:build e2e

package e2e

// The gateway stand-in for this harness. It is deliberately small: it answers
// the v8 management reads Oh My CPA makes on the repository-key path, accepts
// the client-key write approval performs, and gates inference on the access
// list that write produced. A fixture that also served every other console
// route would let a broken request pass by accident.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCPA is a loopback-only CPA v8 stand-in. Its access list starts empty so a
// request that carries any key at all is a failure before approval happens.
type fakeCPA struct {
	server *httptest.Server

	mu             sync.Mutex
	managementKey  string
	clientKeys     []string
	requests       []string
	inferenceCalls []fakeCPAInference
}

type fakeCPAInference struct {
	Authorized bool
	Status     int
	Model      string
}

const fakeCPAManagementKey = "e2e-cpa-management-key"

func startFakeCPA(t *testing.T) *fakeCPA {
	t.Helper()
	g := &fakeCPA{managementKey: fakeCPAManagementKey}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeCPA) baseURL() string { return g.server.URL }

// keys returns a copy of the current access list.
func (g *fakeCPA) keys() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.clientKeys...)
}

func (g *fakeCPA) handle(writer http.ResponseWriter, request *http.Request) {
	g.mu.Lock()
	g.requests = append(g.requests, request.Method+" "+request.URL.Path)
	g.mu.Unlock()

	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/v8/management/config/config-version":
		if !g.authorized(request) {
			writeFixtureJSON(writer, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeFixtureJSON(writer, http.StatusOK, 8)
	case request.Method == http.MethodGet && request.URL.Path == "/v8/management/credentials":
		if !g.authorized(request) {
			writeFixtureJSON(writer, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeFixtureJSON(writer, http.StatusOK, map[string]any{"files": []any{}})
	case request.Method == http.MethodGet && request.URL.Path == "/v8/management/config":
		if !g.authorized(request) {
			writeFixtureJSON(writer, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		g.mu.Lock()
		body := map[string]any{"config-version": 8, "access": map[string]any{"api-keys": append([]string(nil), g.clientKeys...)}}
		g.mu.Unlock()
		writeFixtureJSON(writer, http.StatusOK, body)
	case request.Method == http.MethodPatch && request.URL.Path == "/v8/management/config":
		if !g.authorized(request) {
			writeFixtureJSON(writer, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var patch struct {
			Access struct {
				APIKeys []string `json:"api-keys"`
			} `json:"access"`
		}
		if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
			writeFixtureJSON(writer, http.StatusBadRequest, map[string]any{"error": "invalid_config"})
			return
		}
		g.mu.Lock()
		g.clientKeys = append([]string(nil), patch.Access.APIKeys...)
		g.mu.Unlock()
		writeFixtureJSON(writer, http.StatusOK, map[string]any{"status": "ok", "config-version": 8})
	case request.Method == http.MethodGet && (request.URL.Path == "/v0/management/config.yaml" || request.URL.Path == "/v8/management/config.yaml"):
		if !g.authorized(request) {
			writeFixtureJSON(writer, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		// Already in the v8 layout, so no write converts it and no pre-v8
		// backup gate refuses the approval's configuration write.
		writer.Header().Set("Content-Type", "application/yaml")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("config-version: 8\n"))
	case request.Method == http.MethodGet && request.URL.Path == "/v1/models":
		// The OpenAI-compatible model listing `key exec`/`key install` read for the
		// OpenCode provider. It is gated by the client keys exactly like inference is,
		// because the caller authenticates with the repository key, not the
		// management key.
		token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if !g.clientKeyAuthorized(token) {
			writeFixtureJSON(writer, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeFixtureJSON(writer, http.StatusOK, map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{"id": "gpt-e2e"},
				map[string]any{"id": "claude-e2e"},
			},
		})
	case request.Method == http.MethodPost && request.URL.Path == "/v1/chat/completions":
		g.handleInference(writer, request, false)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/messages":
		// The Anthropic-shaped route is what the `claude` runtime's injected
		// variables are meant to answer, so the same bearer gate covers it.
		g.handleInference(writer, request, true)
	default:
		// An unhandled route is reported rather than guessed at, so a path the
		// real gateway serves differently cannot silently look exercised.
		writeFixtureJSON(writer, http.StatusNotFound, map[string]any{"error": "not found", "path": request.URL.Path})
	}
}

// handleInference is the accept/reject decision the whole flow rests on: the
// bearer must be one of the keys the configuration write actually stored.
func (g *fakeCPA) handleInference(writer http.ResponseWriter, request *http.Request, anthropic bool) {
	token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	var body struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(request.Body).Decode(&body)

	accepted := false
	g.mu.Lock()
	for _, key := range g.clientKeys {
		if key != "" && key == token {
			accepted = true
			break
		}
	}
	g.mu.Unlock()

	status := http.StatusUnauthorized
	if accepted {
		status = http.StatusOK
	}
	g.mu.Lock()
	g.inferenceCalls = append(g.inferenceCalls, fakeCPAInference{Authorized: accepted, Status: status, Model: body.Model})
	g.mu.Unlock()

	if !accepted {
		writeFixtureJSON(writer, status, map[string]any{"error": map[string]any{"type": "invalid_api_key", "message": "the key is not in this gateway's access list"}})
		return
	}
	if anthropic {
		writeFixtureJSON(writer, status, map[string]any{
			"id": "e2e-msg-1", "type": "message", "role": "assistant", "model": body.Model,
			"content":     []any{map[string]any{"type": "text", "text": "E2E_FAKE_GATEWAY_OK"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 7, "output_tokens": 3},
		})
		return
	}
	writeFixtureJSON(writer, status, map[string]any{
		"id": "e2e-chat-1", "object": "chat.completion", "model": body.Model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "E2E_FAKE_GATEWAY_OK"}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
	})
}

func (g *fakeCPA) authorized(request *http.Request) bool {
	return request.Header.Get("Authorization") == "Bearer "+g.managementKey
}

// clientKeyAuthorized reports whether token is one of the keys the configuration write
// stored. It is the gate the real gateway puts on its model listing and inference alike.
func (g *fakeCPA) clientKeyAuthorized(token string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, key := range g.clientKeys {
		if key != "" && key == token {
			return true
		}
	}
	return false
}

func writeFixtureJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
