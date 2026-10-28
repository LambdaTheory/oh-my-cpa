package api

import (
	"net/http"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
)

// Confirmation names the target shown in the console so a stale dialog cannot
// enable a different recipient after another operator changes the settings.
func (f *FeishuAPI) postRecipientConfirmation(writer http.ResponseWriter, request *http.Request) {
	f.settingsMu.Lock()
	defer f.settingsMu.Unlock()
	writer.Header().Set("Cache-Control", "no-store")
	if f.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "feishu storage is unavailable")
		return
	}
	var payload struct {
		Recipient string `json:"recipient"`
	}
	if err := decodeManagementJSON(writer, request, feishuRequestLimit, &payload); err != nil {
		return
	}
	settings, _, err := f.repo.LoadFeishuSettings(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	recipient := settings.ChatID
	if f.overlay.ChatID != nil {
		recipient = strings.TrimSpace(*f.overlay.ChatID)
	}
	if feishu.RecipientKind(recipient) != feishu.RecipientGroup || payload.Recipient != recipient {
		writeError(writer, http.StatusConflict, "recipient changed; review the current group before confirming")
		return
	}
	if f.handler == nil || f.handler.recordAudit(request, "feishu.recipient.confirm", "feishu", "settings", "attempt", nil) != nil {
		writeError(writer, http.StatusServiceUnavailable, "audit storage is unavailable")
		return
	}
	settings.ConfirmedRecipient = recipient
	settings.UpdatedAtMS = f.now().UnixMilli()
	if err := f.repo.SaveFeishuSettings(request.Context(), settings); err != nil {
		writeInternalError(writer, err)
		return
	}
	overlay, err := f.overlayWithStoredSecret(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	f.service.Reload(request.Context(), feishuWebConfig(settings), overlay)
	f.recordAudit(request, "feishu.recipient.confirm", "feishu", "settings", "success", nil)
	writeJSON(writer, http.StatusOK, f.service.StatusSnapshot(request.Context()))
}
