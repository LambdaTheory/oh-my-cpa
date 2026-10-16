package feishu

import (
	"context"
	"fmt"
	"log/slog"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// larkSource is the production long-connection source over the official SDK.
//
// It is the only file that opens a network connection; the service itself depends
// on the EventSource interface, so every test injects a fake and no test connects
// to Feishu.
type larkSource struct {
	appID     string
	appSecret string
	logger    *slog.Logger
}

// newLarkSource builds the websocket source for the resolved credentials.
func newLarkSource(resolved Resolved, logger *slog.Logger) EventSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &larkSource{appID: resolved.AppID, appSecret: resolved.AppSecret, logger: logger}
}

// Name implements EventSource.
func (s *larkSource) Name() string { return "feishu-websocket" }

// Run starts the long connection and blocks until ctx is cancelled.
//
// The SDK reconnects by default (unlimited retries), which is what a long-lived
// bot wants; only reconnect transitions are logged.
//
// IMPORTANT: deploy exactly ONE replica. Feishu caps an app at 50 concurrent long
// connections and delivers each event to just one of them (cluster delivery, not
// broadcast), so extra replicas would split traffic unpredictably.
func (s *larkSource) Run(ctx context.Context, handler EventHandler) error {
	// Long-connection mode requires an empty verification token and encrypt key:
	// there is no HTTP callback route to verify against.
	eventDispatcher := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return handler.HandleMessage(ctx, parseMessage(event))
		}).
		OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
			return nil, handler.HandleCardAction(ctx, parseCardAction(event))
		}).
		OnP2MessageReadV1(handleMessageRead).
		OnP2ChatAccessEventBotP2pChatEnteredV1(ignoreBotP2pChatEntered).
		OnP2ChatDisbandedV1(ignoreChatDisbanded).
		OnP2ChatUpdatedV1(ignoreChatUpdated).
		OnP2ChatMemberBotAddedV1(ignoreChatMemberBotAdded).
		OnP2ChatMemberBotDeletedV1(ignoreChatMemberBotDeleted).
		OnP2ChatMemberUserAddedV1(ignoreChatMemberUserAdded).
		OnP2ChatMemberUserDeletedV1(ignoreChatMemberUserDeleted).
		OnP2ChatMemberUserWithdrawnV1(ignoreChatMemberUserWithdrawn).
		OnP2MessageRecalledV1(ignoreMessageRecalled).
		OnP2MessageReactionCreatedV1(ignoreMessageReactionCreated).
		OnP2MessageReactionDeletedV1(ignoreMessageReactionDeleted)

	client := larkws.NewClient(s.appID, s.appSecret,
		larkws.WithEventHandler(eventDispatcher),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
		larkws.WithOnReconnecting(func() {
			s.logger.InfoContext(ctx, "feishu websocket reconnecting")
		}),
		larkws.WithOnReconnected(func() {
			s.logger.InfoContext(ctx, "feishu websocket reconnected")
		}),
	)
	s.logger.InfoContext(ctx, "feishu long connection starting", "note", "requires single-replica deployment")
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("feishu: long connection: %w", err)
	}
	return nil
}
