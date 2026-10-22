package api

import (
	"net/http"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func (h *Handler) postAccountAutomationUnadopt(writer http.ResponseWriter, request *http.Request) {
	authIndex, ok := accountAutomationAuthIndex(writer, request)
	if !ok {
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	updated, err := h.automation.Unadopt(request.Context(), authIndex)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.automationAccountDTOs(request.Context(), []repository.AutomationAccount{updated})[0])
}

func (h *Handler) postAccountAutomationUnadoptMany(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		AuthIndexes []string `json:"auth_indexes"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if len(payload.AuthIndexes) == 0 || len(payload.AuthIndexes) > accountAutomationAdoptLimit {
		writeError(writer, http.StatusBadRequest, "auth_indexes must contain between 1 and 500 accounts")
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	removed, err := h.automation.UnadoptMany(request.Context(), payload.AuthIndexes)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"removed": removed})
}
