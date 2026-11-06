package capability

import (
	"context"
	"strings"
	"time"
)

type anchorKey struct{}

func WithAnchor(ctx context.Context, anchorMS int64) context.Context {
	return context.WithValue(ctx, anchorKey{}, anchorMS)
}
func AnchorMS(ctx context.Context) int64 {
	if anchor, ok := ctx.Value(anchorKey{}).(int64); ok && anchor > 0 {
		return anchor
	}
	return time.Now().UnixMilli()
}

// readOnlyKey marks a call made through a read-only credential. It is carried on
// the context so a read handler can withhold fields a full-authority caller may
// see (account labels) without every handler learning about the transport's
// principals.
type readOnlyKey struct{}

// WithReadOnly marks a call as coming from a read-only caller. A full-authority
// call is left unmarked, so the zero value means "not read-only".
func WithReadOnly(ctx context.Context, isReadOnly bool) context.Context {
	if !isReadOnly {
		return ctx
	}
	return context.WithValue(ctx, readOnlyKey{}, true)
}

// IsReadOnly reports whether the current call was made by a read-only caller.
func IsReadOnly(ctx context.Context) bool {
	value, _ := ctx.Value(readOnlyKey{}).(bool)
	return value
}

// consoleURLKey carries the address the caller reached this deployment's console at, base
// path included. A capability that answers with a link a person opens (the one-time approval
// link of a repository key application, AC-8) reads it from here instead of guessing a host.
type consoleURLKey struct{}

// WithConsoleURL records the console address for the rest of the call. An empty value leaves
// the context without one and the link out of the answer.
func WithConsoleURL(ctx context.Context, url string) context.Context {
	if strings.TrimSpace(url) == "" {
		return ctx
	}
	return context.WithValue(ctx, consoleURLKey{}, strings.TrimRight(strings.TrimSpace(url), "/"))
}

// ConsoleURLFrom returns the console address of the current call, if the transport knew it.
func ConsoleURLFrom(ctx context.Context) string {
	value, _ := ctx.Value(consoleURLKey{}).(string)
	return value
}
