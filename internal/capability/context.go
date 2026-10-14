package capability

import (
	"context"
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
