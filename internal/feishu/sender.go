package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// receiveIDTypeChatID is the receive_id_type for a group chat. The pinned SDK
// version exposes no constant for it, so it lives here.
const receiveIDTypeChatID = "chat_id"

// Sender is the narrow seam over the Feishu send API so message handling can be
// unit-tested without a network or an app credential. Production uses
// newLarkSender; every test injects a fake.
type Sender interface {
	// SendCard posts a card to the configured person or the dedicated OAuth group.
	SendCard(ctx context.Context, chatID string, card map[string]any) error
	// ReplyText replies to a specific message with plain text.
	ReplyText(ctx context.Context, messageID, text string) error
	// ReplyCard replies to a specific message with an interactive card.
	ReplyCard(ctx context.Context, messageID string, card map[string]any) error
}

// larkSender is the production sender backed by the official SDK.
type larkSender struct {
	client *lark.Client
}

// newLarkSender builds the official-SDK sender. The app secret is held only by
// the client, never by the feishu configuration struct.
func newLarkSender(appID, appSecret string) Sender {
	return &larkSender{client: lark.NewClient(appID, appSecret, lark.WithLogLevel(larkcore.LogLevelInfo))}
}

// SendCard creates an interactive message in chatID.
func (s *larkSender) SendCard(ctx context.Context, chatID string, card map[string]any) error {
	content, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}
	body := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(chatID).
		MsgType(larkim.MsgTypeInteractive).
		Content(string(content)).
		Build()
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType(chatID)).
		Body(body).
		Build()
	resp, err := s.client.Im.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("im.message.create: %w", err)
	}
	if !resp.Success() {
		return recipientDeliveryError(resp.Code, resp.Msg)
	}
	return nil
}

func recipientDeliveryError(code int, message string) error {
	message = strings.ToLower(message)
	switch {
	case strings.Contains(message, "availability") || strings.Contains(message, "visible scope"):
		return fmt.Errorf("这个人用不了这个机器人；在飞书后台把此人加进应用的可用范围（%d）", code)
	case strings.Contains(message, "bot") && (strings.Contains(message, "not in") || strings.Contains(message, "not a member")):
		return fmt.Errorf("机器人不在这个群里；把机器人拉进群后点『发送测试卡片』再试（%d）", code)
	case strings.Contains(message, "not exist") || strings.Contains(message, "not found"):
		return fmt.Errorf("找不到这个群 / 这个人，检查号码是否抄错（%d）", code)
	default:
		return fmt.Errorf("飞书拒收，请检查接收人和应用权限（%d）", code)
	}
}

func receiveIDType(recipient string) string {
	if OAUTH_OPEN_ID.MatchString(recipient) {
		return "open_id"
	}
	return receiveIDTypeChatID
}

// ReplyText answers messageID with a text message.
func (s *larkSender) ReplyText(ctx context.Context, messageID, text string) error {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("marshal text: %w", err)
	}
	body := larkim.NewReplyMessageReqBodyBuilder().
		MsgType(larkim.MsgTypeText).
		Content(string(content)).
		Build()
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(body).
		Build()
	resp, err := s.client.Im.Message.Reply(ctx, req)
	if err != nil {
		return fmt.Errorf("im.message.reply: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("im.message.reply: code=%d msg=%s log_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return nil
}

// ReplyCard answers messageID with an interactive card.
func (s *larkSender) ReplyCard(ctx context.Context, messageID string, card map[string]any) error {
	content, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}
	body := larkim.NewReplyMessageReqBodyBuilder().
		MsgType(larkim.MsgTypeInteractive).
		Content(string(content)).
		Build()
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(body).
		Build()
	resp, err := s.client.Im.Message.Reply(ctx, req)
	if err != nil {
		return fmt.Errorf("im.message.reply: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("im.message.reply: code=%d msg=%s log_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return nil
}

var _ Sender = (*larkSender)(nil)
