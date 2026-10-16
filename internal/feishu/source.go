package feishu

import "context"

// EventHandler receives the parsed events a long-connection source delivers. The
// service implements it; the production websocket source adapts SDK events onto
// it, and tests inject a fake source that never connects.
type EventHandler interface {
	HandleMessage(ctx context.Context, in Inbound) error
	HandleCardAction(ctx context.Context, action CardAction) error
}

// EventSource is the long-connection seam. Run blocks until ctx is cancelled or
// the connection cannot be re-established. The production source is the Lark
// websocket client; a test injects a fake that emits synthetic events.
type EventSource interface {
	Run(ctx context.Context, handler EventHandler) error
	// Name identifies the source in logs.
	Name() string
}
