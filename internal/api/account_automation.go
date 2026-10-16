package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/automation"
	"github.com/oh-my-cpa/oh-my-cpa/internal/operations"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Account-level auto-management API (lane A of the first batch).
//
// The routes sit behind the console's session authentication and its same-origin
// guard, exactly like every other management write; the read-only MCP key reaches
// none of them. The DTOs below are a deliberate allowlist: the roster row carries
// no credential material, only OMC's own plan and the operator's switches.
//
// The list response returns three parts: the resolved switch (settings), the
// persisted managed roster (accounts), and the live gateway credentials offered for
// adoption (candidates). The candidate read never writes the roster. The state and
// monitor_reason vocabularies are canonical and enumerated in ADR 0096: a plan that
// has no recovery instant is reported with monitor_reason "missing_reset", never a
// UI-specific spelling.

const (
	accountAutomationRequestLimit = 64 << 10
	accountAutomationNoteLimit    = 256
	// accountAutomationAdoptLimit bounds one batch selection so a single request
	// cannot adopt an unbounded roster.
	accountAutomationAdoptLimit = 500
)

type accountAutomationAccountDTO struct {
	AuthIndex            string   `json:"auth_index"`
	Provider             string   `json:"provider,omitempty"`
	Name                 string   `json:"name,omitempty"`
	AccountLabel         string   `json:"account_label,omitempty"`
	IsAdopted            bool     `json:"is_adopted"`
	IsEnabled            bool     `json:"is_enabled"`
	State                string   `json:"state"`
	MonitorReason        string   `json:"monitor_reason,omitempty"`
	DisabledByAutomation bool     `json:"disabled_by_automation"`
	RecoveryAtMS         *int64   `json:"recovery_at_ms,omitempty"`
	AdoptedAtMS          *int64   `json:"adopted_at_ms,omitempty"`
	AdoptBatchID         string   `json:"adopt_batch_id,omitempty"`
	NextAttemptAtMS      *int64   `json:"next_attempt_at_ms,omitempty"`
	AttemptCount         int      `json:"attempt_count"`
	LastError            string   `json:"last_error,omitempty"`
	LastDisableAtMS      *int64   `json:"last_disable_at_ms,omitempty"`
	LastEnableAtMS       *int64   `json:"last_enable_at_ms,omitempty"`
	IntentVersion        int64    `json:"intent_version"`
	HasWeeklyEvidence    bool     `json:"has_weekly_evidence"`
	WeeklyUsedPercent    *float64 `json:"weekly_used_percent,omitempty"`
	WeeklyResetAtMS      *int64   `json:"weekly_reset_at_ms,omitempty"`
	WeeklyObservedAtMS   *int64   `json:"weekly_observed_at_ms,omitempty"`
}

type accountAutomationSettingsDTO struct {
	WebEnabled     bool   `json:"web_enabled"`
	Enabled        bool   `json:"enabled"`
	Source         string `json:"source"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	MasterFromEnv  bool   `json:"master_from_env"`
	Accounts       int    `json:"accounts"`
}

// accountAutomationCandidateDTO is one live CPA credential the console can offer
// for adoption. It is read-only safe metadata: the auth index OMC keys by, the
// display name the status patch is addressed by, the provider, the account label
// and whether CPA reports the credential disabled. It carries no credential
// material, no secret and no upstream quota.
type accountAutomationCandidateDTO struct {
	AuthIndex    string `json:"auth_index"`
	Provider     string `json:"provider,omitempty"`
	Name         string `json:"name,omitempty"`
	AccountLabel string `json:"account_label,omitempty"`
	Disabled     bool   `json:"disabled"`
	// Controllable is true when OMC can write the credential's disabled state.
	Controllable bool `json:"controllable"`
	// Adopted reports whether the credential is already on the persisted roster.
	Adopted bool `json:"adopted"`
	// Eligible is true when the candidate can be adopted into automatic control.
	Eligible         bool   `json:"eligible"`
	IneligibleReason string `json:"ineligible_reason,omitempty"`
}

type accountAutomationListResponse struct {
	Settings accountAutomationSettingsDTO  `json:"settings"`
	Accounts []accountAutomationAccountDTO `json:"accounts"`
	// Candidates are the live CPA credentials, so the first load can offer adoption
	// without the roster having to exist first. A GET never writes the roster.
	Candidates []accountAutomationCandidateDTO `json:"candidates"`
	// CandidatesDegraded is set when the live credential list could not be read; the
	// persisted roster is still returned, and Candidates is left empty rather than
	// reported as a successful "no candidates".
	CandidatesDegraded bool   `json:"candidates_degraded"`
	CandidatesError    string `json:"candidates_error,omitempty"`
}

// BuildAutomationService builds the shared service the routes and the background
// loop use. It exists so the api package owns the adaptation between CPA's auth
// files and the automation domain, and the domain package stays free of the HTTP
// and capability clients.
func (h *Handler) BuildAutomationService() *automation.Service {
	service := &automation.Service{
		Repo:   h.repo,
		Logger: h.logger,
		Policy: automation.PolicyConfig{
			ExhaustedUsedPercent: 100,
			MaxEvidenceAge:       h.cfg.Automation.MaxEvidenceAge,
			BackoffBase:          h.cfg.Automation.BackoffBase,
			BackoffMax:           h.cfg.Automation.BackoffMax,
		},
		MasterEnabled: func(webEnabled bool) bool {
			if h.cfg.IsDemoMode {
				return false
			}
			return h.cfg.Automation.EffectiveEnabled(webEnabled)
		},
		Source: func(webEnabled bool) string {
			if h.cfg.IsDemoMode {
				return "demo"
			}
			return h.cfg.Automation.Source(webEnabled)
		},
		Credentials: h.automationCredentials,
		SetDisabled: func(ctx context.Context, name, authIndex string, disabled bool) error {
			return h.operationsService().SetCredentialStatus(ctx,
				operations.CredentialTarget{Name: name, AuthIndex: authIndex}, disabled, "")
		},
		Audit: func(ctx context.Context, action, result, targetID string, details map[string]any) {
			h.recordAuditWithoutRequest(action, "account_automation", targetID, result, details)
		},
	}
	return service
}

// automationCredentials lists the gateway's auth files projected to what the
// automation domain needs. A credential is controllable when it is an auth file
// with a name, which is what CPA's status patch is addressed by.
func (h *Handler) automationCredentials(ctx context.Context) ([]automation.Credential, error) {
	client, err := h.capabilityClient(ctx)
	if err != nil {
		return nil, err
	}
	files, err := client.AuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	credentials := make([]automation.Credential, 0, len(files.Files))
	for _, file := range files.Files {
		authIndex := strings.TrimSpace(file.AuthIndex)
		if authIndex == "" {
			continue
		}
		label := strings.TrimSpace(file.Email)
		if label == "" {
			label = strings.TrimSpace(file.Label)
		}
		credentials = append(credentials, automation.Credential{
			AuthIndex:    authIndex,
			Name:         strings.TrimSpace(file.Name),
			Provider:     quota.DetectProvider(file.Type, file.Provider),
			AccountLabel: label,
			Disabled:     file.Disabled,
			Controllable: strings.TrimSpace(file.Name) != "",
		})
	}
	return credentials, nil
}

func (h *Handler) automationSettings(ctx context.Context) accountAutomationSettingsDTO {
	if h.automation == nil {
		return accountAutomationSettingsDTO{
			Source:         "unavailable",
			DisabledReason: "unavailable",
			MasterFromEnv:  h.cfg.Automation.MasterFromEnv,
		}
	}
	status, err := h.automation.Status(ctx)
	if err != nil {
		h.logger.Warn("account automation: status read failed", "error", err)
		return accountAutomationSettingsDTO{Source: "error", DisabledReason: "error"}
	}
	settings := accountAutomationSettingsDTO{
		WebEnabled:     status.WebEnabled,
		Enabled:        status.Enabled,
		Source:         status.Source,
		DisabledReason: status.DisabledReason,
		MasterFromEnv:  h.cfg.Automation.MasterFromEnv,
	}
	if h.repo != nil {
		if count, err := h.repo.CountAutomationAccounts(ctx); err == nil {
			settings.Accounts = int(count)
		}
	}
	return settings
}

func (h *Handler) getAccountAutomation(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	settings := h.automationSettings(request.Context())
	accounts := []accountAutomationAccountDTO{}
	roster := []repository.AutomationAccount{}
	if h.automation != nil {
		listed, err := h.automation.ListAccounts(request.Context())
		if err != nil {
			writeInternalError(writer, err)
			return
		}
		roster = listed
		accounts = h.automationAccountDTOs(request.Context(), roster)
	}
	// Candidates are the live CPA credentials, read through the gateway's own
	// management list. This is a read: it never adopts anything, so the first load can
	// offer adoption before the roster exists. The same list is what an adoption then
	// selects from, so it adds no upstream quota read.
	adopted := make(map[string]bool, len(roster))
	for _, account := range roster {
		adopted[account.AuthIndex] = true
	}
	candidates := []accountAutomationCandidateDTO{}
	degraded := false
	candidatesError := ""
	if credentials, err := h.automationCredentials(request.Context()); err != nil {
		// A failed gateway read must read as degraded, not as "no credentials": an
		// empty candidate list would hide a broken gateway behind a success.
		degraded = true
		candidatesError = "credential list unavailable"
		h.logger.Warn("account automation: candidate list failed", "error", err)
	} else {
		candidates = automationCandidateDTOs(credentials, adopted)
	}
	writeJSON(writer, http.StatusOK, accountAutomationListResponse{
		Settings:           settings,
		Accounts:           accounts,
		Candidates:         candidates,
		CandidatesDegraded: degraded,
		CandidatesError:    candidatesError,
	})
}

// automationCandidateDTOs projects the live credentials to their safe adoption
// metadata. Eligibility requires both that CPA controllability holds (a credential
// with no name cannot be addressed by the status patch) and that the provider's
// account-level weekly window is a capability OMC has verified. An unverified
// provider is shown ineligible rather than offered as if any seven-day-shaped window
// it published could be acted on; the reason names the unverified capability.
func automationCandidateDTOs(credentials []automation.Credential, adopted map[string]bool) []accountAutomationCandidateDTO {
	result := make([]accountAutomationCandidateDTO, 0, len(credentials))
	for _, credential := range credentials {
		candidate := accountAutomationCandidateDTO{
			AuthIndex:    credential.AuthIndex,
			Provider:     credential.Provider,
			Name:         credential.Name,
			AccountLabel: credential.AccountLabel,
			Disabled:     credential.Disabled,
			Controllable: credential.Controllable,
			Adopted:      adopted[credential.AuthIndex],
			Eligible:     credential.Controllable,
		}
		switch {
		case !credential.Controllable:
			candidate.Eligible = false
			candidate.IneligibleReason = automation.ReasonNoCredential
		case !automation.ProviderHasVerifiedWeeklyCapability(credential.Provider):
			candidate.Eligible = false
			candidate.IneligibleReason = automation.ReasonUnverifiedCapability
		}
		result = append(result, candidate)
	}
	return result
}

func (h *Handler) getAccountAutomationSettings(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, h.automationSettings(request.Context()))
}

// automationAccountDTOs joins each roster row with the newest weekly reading and
// the recovery instant so the console can explain a plan without a second call.
func (h *Handler) automationAccountDTOs(ctx context.Context, roster []repository.AutomationAccount) []accountAutomationAccountDTO {
	indexes := make([]string, 0, len(roster))
	for _, account := range roster {
		indexes = append(indexes, account.AuthIndex)
	}
	evidence := map[string]repository.AutomationWeeklyWindow{}
	if h.repo != nil {
		if latest, err := h.repo.LatestStandardWeeklyWindows(ctx, indexes); err == nil {
			evidence = latest
		} else {
			h.logger.Warn("account automation: evidence read failed", "error", err)
		}
	}
	result := make([]accountAutomationAccountDTO, 0, len(roster))
	for _, account := range roster {
		dto := accountAutomationAccountDTO{
			AuthIndex:            account.AuthIndex,
			Provider:             account.Provider,
			Name:                 account.Name,
			AccountLabel:         account.AccountLabel,
			IsAdopted:            account.IsAdopted,
			IsEnabled:            account.IsEnabled,
			State:                account.State,
			MonitorReason:        account.MonitorReason,
			DisabledByAutomation: account.IsDisabledByAutomation(),
			AdoptedAtMS:          account.AdoptedAtMS,
			AdoptBatchID:         account.AdoptBatchID,
			NextAttemptAtMS:      account.NextAttemptAtMS,
			AttemptCount:         account.AttemptCount,
			LastError:            account.LastError,
			LastDisableAtMS:      account.LastDisableAtMS,
			LastEnableAtMS:       account.LastEnableAtMS,
			IntentVersion:        account.IntentVersion,
		}
		if dueMS, ok := automation.RecoveryDue(account); ok {
			dto.RecoveryAtMS = &dueMS
		}
		if window, ok := evidence[account.AuthIndex]; ok {
			dto.HasWeeklyEvidence = true
			usedPercent := window.UsedPercent
			dto.WeeklyUsedPercent = &usedPercent
			dto.WeeklyResetAtMS = window.ResetAtMS
			observedAt := window.ObservedAtMS
			dto.WeeklyObservedAtMS = &observedAt
		}
		result = append(result, dto)
	}
	return result
}

func (h *Handler) putAccountAutomationSettings(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if payload.Enabled == nil {
		writeError(writer, http.StatusBadRequest, "enabled is required")
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	if err := h.automation.SetWebEnabled(request.Context(), *payload.Enabled); err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.automationSettings(request.Context()))
}

func (h *Handler) putAccountAutomationAccount(writer http.ResponseWriter, request *http.Request) {
	authIndex, ok := accountAutomationAuthIndex(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if payload.Enabled == nil {
		writeError(writer, http.StatusBadRequest, "enabled is required")
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	updated, err := h.automation.SetAccountSwitch(request.Context(), authIndex, *payload.Enabled)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.automationAccountDTOs(request.Context(), []repository.AutomationAccount{updated})[0])
}

func (h *Handler) postAccountAutomationAdopt(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		AuthIndexes []string `json:"auth_indexes"`
		Enable      bool     `json:"enable"`
		Note        string   `json:"note"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if len(payload.AuthIndexes) == 0 {
		writeError(writer, http.StatusBadRequest, "auth_indexes is required")
		return
	}
	if len(payload.AuthIndexes) > accountAutomationAdoptLimit {
		writeError(writer, http.StatusBadRequest, "too many accounts in one batch")
		return
	}
	note := strings.TrimSpace(payload.Note)
	if len(note) > accountAutomationNoteLimit {
		note = note[:accountAutomationNoteLimit]
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	adopted, err := h.automation.Adopt(request.Context(), payload.AuthIndexes, payload.Enable, note)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"adopted": adopted})
}

func (h *Handler) postAccountAutomationCancel(writer http.ResponseWriter, request *http.Request) {
	authIndex, ok := accountAutomationAuthIndex(writer, request)
	if !ok {
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	updated, err := h.automation.CancelAccount(request.Context(), authIndex)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.automationAccountDTOs(request.Context(), []repository.AutomationAccount{updated})[0])
}

func (h *Handler) postAccountAutomationManualReset(writer http.ResponseWriter, request *http.Request) {
	authIndex, ok := accountAutomationAuthIndex(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ResetAtMS int64 `json:"reset_at_ms"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	// A recovery time must be in the future; a past instant would schedule an
	// immediate enable, which is the operator's to ask for as a switch, not a
	// "recovery time".
	if payload.ResetAtMS <= h.now().UnixMilli() {
		writeError(writer, http.StatusBadRequest, "reset_at_ms must be in the future")
		return
	}
	if h.automation == nil {
		writeError(writer, http.StatusServiceUnavailable, "account automation is unavailable")
		return
	}
	updated, err := h.automation.SetManualReset(request.Context(), authIndex, payload.ResetAtMS)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.automationAccountDTOs(request.Context(), []repository.AutomationAccount{updated})[0])
}

func accountAutomationAuthIndex(writer http.ResponseWriter, request *http.Request) (string, bool) {
	authIndex := strings.TrimSpace(chi.URLParam(request, "authIndex"))
	if authIndex == "" || len(authIndex) > 256 {
		writeError(writer, http.StatusBadRequest, "invalid auth_index")
		return "", false
	}
	return authIndex, true
}

func (h *Handler) writeAccountAutomationError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automation.ErrAccountNotFound):
		writeError(writer, http.StatusNotFound, "account is not adopted")
	case errors.Is(err, automation.ErrUnverifiedWeeklyCapability):
		writeError(writer, http.StatusBadRequest, "provider's account-level weekly window is not verified")
	case errors.Is(err, repository.ErrAutomationConflict):
		writeError(writer, http.StatusConflict, "account state changed; reload and retry")
	case err.Error() == "write_busy":
		writeError(writer, http.StatusConflict, "another write is in progress")
	default:
		writeCPAFacadeError(writer, err)
	}
}
