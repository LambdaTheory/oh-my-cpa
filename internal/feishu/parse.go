package feishu

import (
	"encoding/json"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Inbound is the flattened, nil-safe view of a message event. The service acts on
// this rather than on the SDK type, so tests can drive the handlers without
// constructing SDK events or reaching the network.
type Inbound struct {
	EventID      string
	MessageID    string
	ChatID       string
	ChatType     string // "p2p" for a one-to-one chat, "group" otherwise
	SenderOpenID string
	// FromBot is true when the sender is a bot. The bot ignores its own messages
	// so it can never reply to itself and loop.
	FromBot      bool
	Text         string
	BotMentioned bool
}

// IsDirect reports a one-to-one chat with the bot: every message there is
// addressed to it, so no @mention is needed.
func (in Inbound) IsDirect() bool { return in.ChatType == "p2p" }

// CardAction is the flattened view of a card button callback. Action is untrusted
// input used only to choose a branch, never as a credential.
type CardAction struct {
	EventID        string
	MessageID      string
	ChatID         string
	OperatorOpenID string
	Action         string
}

// parseMessage extracts the fields the bot acts on. Every SDK field is a pointer,
// so each is nil-checked before dereferencing.
func parseMessage(event *larkim.P2MessageReceiveV1) Inbound {
	var in Inbound
	if event == nil || event.Event == nil {
		return in
	}
	in.EventID = eventID(event.EventV2Base)
	if message := event.Event.Message; message != nil {
		if message.ChatId != nil {
			in.ChatID = *message.ChatId
		}
		if message.MessageId != nil {
			in.MessageID = *message.MessageId
		}
		if message.ChatType != nil {
			in.ChatType = *message.ChatType
		}
		for _, mention := range message.Mentions {
			if mention != nil && mention.MentionedType != nil && *mention.MentionedType == "bot" {
				in.BotMentioned = true
				break
			}
		}
		if message.MessageType != nil && *message.MessageType == larkim.MsgTypeText && message.Content != nil {
			in.Text = extractText(*message.Content)
		}
	}
	if sender := event.Event.Sender; sender != nil {
		if sender.SenderId != nil && sender.SenderId.OpenId != nil {
			in.SenderOpenID = *sender.SenderId.OpenId
		}
		if sender.SenderType != nil && *sender.SenderType == "bot" {
			in.FromBot = true
		}
	}
	return in
}

// parseCardAction extracts the fields a card callback acts on.
func parseCardAction(event *callback.CardActionTriggerEvent) CardAction {
	var action CardAction
	if event == nil || event.Event == nil {
		return action
	}
	action.EventID = eventID(event.EventV2Base)
	if event.Event.Context != nil {
		action.MessageID = event.Event.Context.OpenMessageID
		action.ChatID = event.Event.Context.OpenChatID
	}
	if event.Event.Operator != nil {
		action.OperatorOpenID = event.Event.Operator.OpenID
	}
	if event.Event.Action != nil {
		if value, ok := event.Event.Action.Value["action"].(string); ok {
			action.Action = value
		}
	}
	return action
}

// extractText decodes the "text" field from a text message's JSON content.
func extractText(content string) string {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ""
	}
	return payload.Text
}

// eventID returns the platform event id, which is stable across redeliveries of
// the same event.
func eventID(base *larkevent.EventV2Base) string {
	if base == nil || base.Header == nil {
		return ""
	}
	return base.Header.EventID
}

// messageEventKey is the dedup key for an inbound message event. The event id is
// the authoritative identity; the message id is the fallback.
func messageEventKey(eventIDValue, messageID string) string {
	if eventIDValue != "" {
		return "msg:" + eventIDValue
	}
	if messageID != "" {
		return "msgid:" + messageID
	}
	return ""
}

// cardEventKey is the dedup key for a card callback. The event id is the primary
// identity; the message plus action is the fallback.
func cardEventKey(action CardAction) string {
	if action.EventID != "" {
		return "card:" + action.EventID
	}
	if action.MessageID == "" {
		return ""
	}
	return "card:" + action.MessageID + ":" + action.Action
}
