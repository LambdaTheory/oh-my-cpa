package capability

import "context"

// Caller is who is asking, for the few capabilities whose answer depends on the agent and
// machine behind the shared management credential rather than on the console operator.
//
// A management key identifies the deployment, not the agent: every MCP client connected to
// one OMC presents the same credential. Features that must record "this agent, on this
// machine" therefore carry it as self-reported caller data, which the operator confirms once
// and the audit trail remembers (PRD plans/prd/omc-repo-api-keys, 4.2).
//
// It is deliberately not part of Principal: a principal is an authorization decision made by
// the server, while a caller is a claim made by the client, and nothing may be granted on it.
type Caller struct {
	Agent  string
	Device string
}

type callerKey struct{}

// WithCaller attaches the requesting agent and device to a tool call's context.
func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFrom reads the caller a transport attached. ok is false when the capability was
// invoked through a path that carries no caller identity at all; such a request cannot be
// turned into an application, because there would be nobody to deliver a key to.
func CallerFrom(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller)
	return caller, ok
}
