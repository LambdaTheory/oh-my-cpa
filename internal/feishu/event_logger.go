package feishu

import (
	"context"
	"log/slog"
)

// SDK diagnostics can contain event bodies or decoder errors. The bot owns its
// lifecycle logs; only fixed failure categories cross this adapter.
type eventLogger struct{ logger *slog.Logger }

func (l eventLogger) Debug(context.Context, ...interface{}) {}
func (l eventLogger) Info(context.Context, ...interface{})  {}
func (l eventLogger) Warn(ctx context.Context, _ ...interface{}) {
	l.logger.WarnContext(ctx, "feishu event transport warning")
}
func (l eventLogger) Error(ctx context.Context, _ ...interface{}) {
	l.logger.ErrorContext(ctx, "feishu event transport failure")
}
