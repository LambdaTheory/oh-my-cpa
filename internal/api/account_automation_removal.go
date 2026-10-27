package api

import (
	"net/http"

	"github.com/oh-my-cpa/oh-my-cpa/internal/automation"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func (h *Handler) postAccountAutomationUnadopt(writer http.ResponseWriter, request *http.Request) {
	authIndex, ok := accountAutomationAuthIndex(writer, request)
	if !ok {
		return
	}
	var payload struct {
		// ExpectedIntentVersion names the lifecycle the console read, so a duplicated request
		// cannot remove the membership of a credential adopted again under the same index.
		ExpectedIntentVersion *int64 `json:"expected_intent_version"`
	}
	if err := decodeOptionalManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if payload.ExpectedIntentVersion != nil && *payload.ExpectedIntentVersion < 0 {
		writeError(writer, http.StatusBadRequest, "expected_intent_version must not be negative")
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	// Unadoption is local intent: it never reads the gateway, so it stays available during an
	// inventory outage and never changes the credential's enabled or disabled state.
	updated, err := h.automation.UnadoptExpected(request.Context(), authIndex, payload.ExpectedIntentVersion)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.automationAccountDTOs(request.Context(), []repository.AutomationAccount{updated})[0])
}

func (h *Handler) postAccountAutomationUnadoptMany(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		AuthIndexes []string `json:"auth_indexes"`
		// Targets carries the per-account lifecycle versions the console read. The legacy
		// auth_indexes list is still accepted; a selection named only there has no version to
		// check, and is documented as unfenced rather than silently protected.
		Targets []struct {
			AuthIndex             string `json:"auth_index"`
			ExpectedIntentVersion *int64 `json:"expected_intent_version"`
		} `json:"targets"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if payload.AuthIndexes == nil {
		for _, target := range payload.Targets {
			payload.AuthIndexes = append(payload.AuthIndexes, target.AuthIndex)
		}
	}
	if len(payload.AuthIndexes) == 0 || len(payload.AuthIndexes) > accountAutomationAdoptLimit {
		writeError(writer, http.StatusBadRequest, "auth_indexes must contain between 1 and 500 accounts")
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	targets := make([]automation.UnadoptTarget, 0, len(payload.AuthIndexes))
	versions := make(map[string]*int64, len(payload.Targets))
	for _, target := range payload.Targets {
		if target.ExpectedIntentVersion == nil {
			continue
		}
		if *target.ExpectedIntentVersion < 0 {
			writeError(writer, http.StatusBadRequest, "expected_intent_version must not be negative")
			return
		}
		versions[target.AuthIndex] = target.ExpectedIntentVersion
	}
	for _, authIndex := range payload.AuthIndexes {
		targets = append(targets, automation.UnadoptTarget{AuthIndex: authIndex, ExpectedIntentVersion: versions[authIndex]})
	}
	removed, err := h.automation.UnadoptManyExpected(request.Context(), targets)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"removed": removed})
}
