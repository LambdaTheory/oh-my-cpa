package feishu

import (
	"context"
	"errors"
	"html"
	"regexp"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/reauth"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

var OAUTH_OPEN_ID = regexp.MustCompile(`^ou_[A-Za-z0-9_-]+$`)

// ConfigureOAuth is called before starting the shared event source.
func (s *Service) ConfigureOAuth(chatID, contactID string, accept func(context.Context, string, string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauthChatID = chatID
	s.oauthContactOpenID = contactID
	s.oauthCodeHandler = accept
}
func (s *Service) OAuthReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolved.ConnectionReady() && s.oauthChatID != "" && OAUTH_OPEN_ID.MatchString(s.oauthContactOpenID)
}

// The dedicated group never falls through to queries, including malformed or
// mentioned codes. No rejected message body reaches logging or persistence.
func (s *Service) handleOAuthMessage(ctx context.Context, in Inbound) bool {
	s.mu.RLock()
	chatID, contactID, accept := s.oauthChatID, s.oauthContactOpenID, s.oauthCodeHandler
	s.mu.RUnlock()
	if chatID == "" || in.ChatID != chatID {
		return false
	}
	if !s.OAuthReady() || in.FromBot || in.ChatType != "group" || in.SenderOpenID != contactID || in.MessageType != "text" || accept == nil {
		return true
	}
	codeCtx, cancel := context.WithTimeout(ctx, defaultReplyBudget)
	defer cancel()
	if err := accept(codeCtx, in.MessageID, in.Text); err != nil {
		s.logger.WarnContext(ctx, "reauth code acceptance failed", "reason", "storage_unavailable")
	}
	return true
}
func (s *Service) SendOAuthCard(ctx context.Context, j repository.ReauthJob, kind string) error {
	if !s.OAuthReady() {
		return errors.New("reauth_disabled")
	}
	s.mu.RLock()
	sender, chatID, contactID := s.sender, s.oauthChatID, s.oauthContactOpenID
	s.mu.RUnlock()
	if sender == nil {
		return errors.New("reauth_sender_unavailable")
	}
	return sender.SendCard(ctx, chatID, OAuthCard(j, kind, contactID))
}
func OAuthCard(j repository.ReauthJob, kind, contactID string) map[string]any {
	provider, _ := reauth.ProviderFor(j.Provider)
	title, text, color := provider.Name+" 重授权", "", "blue"
	switch kind {
	case "request":
		mention := `<at id_type="open_id" id="` + html.EscapeString(contactID) + `">接码人</at> `
		switch j.Flow {
		case "redirect_otp":
			text = mention + "请直接发送 " + provider.Name + " 登录邮件中的 6 位验证码。"
		case "redirect_browser":
			text = mention + "请在浏览器完成 Google 登录，完成后由登录程序确认。"
		case "redirect_manual_callback":
			text = mention + "请在浏览器完成登录。回调需可达 CPA 自身端口；远端部署请建立隧道，或将完整 callback URL 手工粘贴到登录程序。"
		case "device_code":
			text = mention + "请打开验证页面并输入设备码：**" + html.EscapeString(j.UserCode) + "**\n验证页面：" + html.EscapeString(j.VerificationURL) + "\n过期时间：" + time.UnixMilli(j.ExpiresAtMS).UTC().Format(time.RFC3339)
		}
	case "processing":
		text = "已收到验证码，正在处理重授权。"
	case "succeeded":
		text = "重授权完成，额度刷新已确认凭证恢复。"
		color = "green"
	case "failed":
		text = "重授权未恢复，请检查登录流程。原因：" + html.EscapeString(j.LastError)
		color = "red"
	}
	return map[string]any{"schema": "2.0", "header": map[string]any{"title": map[string]any{"tag": "plain_text", "content": title + " · " + j.AccountLabel}, "template": color}, "body": map[string]any{"elements": []map[string]any{{"tag": "markdown", "content": text}}}}
}
