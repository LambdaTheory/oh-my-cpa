package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/mcpbridge"
)

// MCP_CONSOLE_URL_HEADER carries the console address of the request being served to the tool
// handlers, which see the request's headers but not its host. serveMCP overwrites whatever the
// caller sent under this name.
const MCP_CONSOLE_URL_HEADER = "X-Omc-Console-Url"

// executorBackend is the in-process side of the MCP endpoint: the same executor calls the
// capability HTTP endpoints make, without a second HTTP hop. The principal is fixed when the
// server is built, so a read-only server can list only the tools a read-only caller may run.
type executorBackend struct {
	handler   *Handler
	principal capability.Principal
}

func (backend executorBackend) Catalog(context.Context) ([]*capability.Definition, error) {
	return backend.handler.agent.executor.Registry.List(backend.principal), nil
}
func (backend executorBackend) Invoke(ctx context.Context, name string, arguments json.RawMessage) (capability.Result, error) {
	h := backend.handler
	result, err := h.agent.executor.Invoke(ctx, backend.principal, name, arguments, "")
	if err != nil {
		return result, errors.New(capability.ErrorCode(err))
	}
	return result, nil
}
func (backend executorBackend) Operation(ctx context.Context, id string) (capability.Operation, error) {
	h := backend.handler
	operation, err := h.agent.executor.Get(ctx, backend.principal, id)
	if err != nil {
		return operation, errors.New("operation_not_found")
	}
	return operation, nil
}

// consoleURL is the address the caller reached this console at, base path included. It is
// only ever returned to that same authenticated caller, as the approval link.
func (h *Handler) consoleURL(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	} else if forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + request.Host + strings.TrimRight(h.cfg.BasePath, "/")
}

func (h *Handler) mcpHandler(isReadOnly bool) (http.Handler, error) {
	h.agent.mcpMu.Lock()
	defer h.agent.mcpMu.Unlock()
	handler := h.agent.mcp
	if isReadOnly {
		handler = h.agent.mcpReadOnly
	}
	if handler != nil {
		return handler, nil
	}
	principal := h.mcpPrincipal()
	if isReadOnly {
		principal = h.readOnlyPrincipal()
	}
	server, err := mcpbridge.NewServer(context.Background(), executorBackend{handler: h, principal: principal}, mcpbridge.Options{Version: h.cfg.Version, ConsoleURL: func(request *mcp.CallToolRequest) string {
		if request.Extra == nil {
			return ""
		}
		return request.Extra.Header.Get(MCP_CONSOLE_URL_HEADER)
	}})
	if err != nil {
		return nil, err
	}
	handler = mcpbridge.NewHTTPHandler(func(*http.Request) *mcp.Server { return server })
	if isReadOnly {
		h.agent.mcpReadOnly = handler
	} else {
		h.agent.mcp = handler
	}
	return handler, nil
}

// serveMCP is the capability registry as a Streamable HTTP MCP endpoint (ADR 0070).
//
// It accepts the management key as a bearer token and nothing else: a session cookie is not
// an MCP credential, so a page open in the operator's browser cannot drive the endpoint. A
// configured read-only key reaches a second server that offers only read tools.
func (h *Handler) serveMCP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if request.Header.Get("Authorization") == "" {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="oh-my-cpa"`)
		writePlaygroundError(writer, 401, "authentication_required")
		return
	}
	if !h.authenticateBearer(writer, request) {
		return
	}
	handler, err := h.mcpHandler(h.readOnlyBearer(request))
	if err != nil {
		writePlaygroundError(writer, 503, "capability_unavailable")
		return
	}
	request.Header.Set(MCP_CONSOLE_URL_HEADER, h.consoleURL(request))
	handler.ServeHTTP(writer, request)
}
