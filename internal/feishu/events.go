package feishu

import (
	"context"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// This file registers silent handlers for Feishu event types the bot does not act
// on. The SDK logs a "not found handler" error for every pushed event with no
// registered handler, so an unhandled read receipt or "user entered the bot chat"
// line floods the error log with noise that hides real failures.
//
// Each handler is a deliberate no-op: it returns nil without touching the network.
// Only IM (messaging) events are listed; registering a handler for an event the
// app never receives is harmless, while registering none for one it does receive
// is the bug fixed here.

func handleMessageRead(_ context.Context, _ *larkim.P2MessageReadV1) error { return nil }

func ignoreBotP2pChatEntered(_ context.Context, _ *larkim.P2ChatAccessEventBotP2pChatEnteredV1) error {
	return nil
}

func ignoreChatDisbanded(_ context.Context, _ *larkim.P2ChatDisbandedV1) error { return nil }

func ignoreChatUpdated(_ context.Context, _ *larkim.P2ChatUpdatedV1) error { return nil }

func ignoreChatMemberBotAdded(_ context.Context, _ *larkim.P2ChatMemberBotAddedV1) error {
	return nil
}

func ignoreChatMemberBotDeleted(_ context.Context, _ *larkim.P2ChatMemberBotDeletedV1) error {
	return nil
}

func ignoreChatMemberUserAdded(_ context.Context, _ *larkim.P2ChatMemberUserAddedV1) error {
	return nil
}

func ignoreChatMemberUserDeleted(_ context.Context, _ *larkim.P2ChatMemberUserDeletedV1) error {
	return nil
}

func ignoreChatMemberUserWithdrawn(_ context.Context, _ *larkim.P2ChatMemberUserWithdrawnV1) error {
	return nil
}

func ignoreMessageRecalled(_ context.Context, _ *larkim.P2MessageRecalledV1) error { return nil }

func ignoreMessageReactionCreated(_ context.Context, _ *larkim.P2MessageReactionCreatedV1) error {
	return nil
}

func ignoreMessageReactionDeleted(_ context.Context, _ *larkim.P2MessageReactionDeletedV1) error {
	return nil
}
