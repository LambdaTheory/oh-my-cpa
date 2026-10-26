package api

import (
	"context"
	"errors"
	"fmt"
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
// The list response describes the gateway's current credentials and the plans matched to
// them: the resolved switch (settings), the inventory's availability and counts, one row
// per current credential with its plan when uniquely matched, and the active roster for
// callers that reason about plans. A read never adopts, cleans up or moves an absence
// counter. The state and monitor_reason vocabularies are canonical and enumerated in ADR 0096: a plan that
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
	BurnCredit           bool     `json:"burn_credit"`
	Removed              bool     `json:"removed"`
	State                string   `json:"state"`
	MonitorReason        string   `json:"monitor_reason,omitempty"`
	AutoDisableSupported bool     `json:"auto_disable_supported"`
	DisabledByAutomation bool     `json:"disabled_by_automation"`
	RecoveryAtMS         *int64   `json:"recovery_at_ms,omitempty"`
	RecoveryStatus       string   `json:"recovery_status,omitempty"`
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

type accountAutomationListResponse struct {
	Settings accountAutomationSettingsDTO `json:"settings"`
	// Inventory is the availability and truthful counts of the gateway's current
	// credentials. Counts are unknown (nil) when the list could not be read.
	Inventory accountAutomationInventoryDTO `json:"inventory"`
	// Credentials are the gateway's current credentials, each with the managed plan that
	// matched it (or none). This is the console's main table: a stored plan without a
	// current credential is never a row here.
	Credentials []accountAutomationCredentialDTO `json:"credentials"`
	// Accounts remains the active managed roster with each plan, for the read-only
	// Agent/MCP summary and callers that reason about plans rather than credentials.
	Accounts []accountAutomationAccountDTO `json:"accounts"`
	// AccountsLastKnown is set when the inventory could not be read: Accounts are then the
	// last persisted plans, not a statement about the current gateway.
	AccountsLastKnown bool `json:"accounts_last_known"`
	// Candidates is the same current list projected as adoption metadata without plans,
	// so a caller that only selects targets keeps one shape. It is always present; an empty
	// list is ambiguous on its own, so callers must read Inventory.Available before treating
	// it as "no candidates".
	Candidates []accountAutomationCandidateDTO `json:"candidates"`
}

// accountAutomationInventoryDTO is the safe current-inventory summary. Every count is a
// pointer so "unknown" (a failed read) and "zero" (a complete empty gateway) stay distinct
// on the wire, which is the difference between an outage and an empty gateway.
type accountAutomationInventoryDTO struct {
	Available bool `json:"available"`
	// Error is a stable, non-secret reason the inventory is unavailable.
	Error string `json:"error,omitempty"`
	// Credentials is the total of current credentials, disabled and unmanaged included.
	Credentials *int `json:"credentials,omitempty"`
	// Managed and Unmanaged partition Credentials by a uniquely matched plan.
	Managed   *int `json:"managed,omitempty"`
	Unmanaged *int `json:"unmanaged,omitempty"`
	// Disabled counts current credentials CPA itself reports disabled.
	Disabled *int `json:"disabled,omitempty"`
	// PendingAbsence counts active plans whose credential is not in the current inventory.
	// They are awaiting the second complete confirmation and are not rows. It is a pointer
	// for the same reason as the others: during an outage nothing is "pending", so publishing
	// 0 would be a claim the failed read cannot support.
	PendingAbsence *int `json:"pending_absence,omitempty"`
}

// accountAutomationCredentialDTO is one current gateway credential joined with its matched
// plan. It is a read-only allowlist: no credential material, only the addressing OMC uses and
// the plan the operator set for it.
type accountAutomationCredentialDTO struct {
	AuthIndex    string `json:"auth_index"`
	Provider     string `json:"provider,omitempty"`
	Name         string `json:"name,omitempty"`
	AccountLabel string `json:"account_label,omitempty"`
	Disabled     bool   `json:"disabled"`
	Controllable bool   `json:"controllable"`
	// Managed reports a uniquely matched active plan for this credential.
	Managed bool `json:"managed"`
	// Adoptable reports whether this credential can join the roster. Provider capability is
	// deliberately separate: adoption may be monitor-only.
	Adoptable             bool   `json:"adoptable"`
	AdoptionBlockedReason string `json:"adoption_blocked_reason,omitempty"`
	// AutoDisableSupported is verified provider capability, not current state.
	AutoDisableSupported bool `json:"auto_disable_supported"`
	// Plan is the matched plan, present only when Managed.
	Plan *accountAutomationAccountDTO `json:"plan,omitempty"`
}

// accountAutomationCandidateDTO is retained for callers that selected from the live list
// before the current-inventory projection existed; it is the same safe metadata as one
// credential row without its plan.
type accountAutomationCandidateDTO struct {
	AuthIndex             string `json:"auth_index"`
	Provider              string `json:"provider,omitempty"`
	Name                  string `json:"name,omitempty"`
	AccountLabel          string `json:"account_label,omitempty"`
	Disabled              bool   `json:"disabled"`
	Controllable          bool   `json:"controllable"`
	Adopted               bool   `json:"adopted"`
	Adoptable             bool   `json:"adoptable"`
	AdoptionBlockedReason string `json:"adoption_blocked_reason,omitempty"`
	AutoDisableSupported  bool   `json:"auto_disable_supported"`
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
// with a name, which is what CPA's status patch is addressed by. The list is not
// validated here: the shared complete-inventory validator decides whether it is
// authoritative, so every caller reaches the same verdict.
func (h *Handler) automationCredentials(ctx context.Context) ([]automation.Credential, error) {
	client, err := h.capabilityClient(ctx)
	if err != nil {
		return nil, err
	}
	files, err := client.AuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	if files.Files == nil {
		// The contract has no total or page field; a missing files array is the one
		// incompleteness signal OMC can decode. An unmarked omission cannot be detected.
		return nil, fmt.Errorf("credential list unavailable: missing files array")
	}
	credentials := make([]automation.Credential, 0, len(files.Files))
	for _, file := range files.Files {
		label := strings.TrimSpace(file.Email)
		if label == "" {
			label = strings.TrimSpace(file.Label)
		}
		credentials = append(credentials, automation.Credential{
			AuthIndex:    strings.TrimSpace(file.AuthIndex),
			Name:         strings.TrimSpace(file.Name),
			Provider:     quota.DetectProvider(file.Type, file.Provider),
			AccountLabel: label,
			Disabled:     file.Disabled,
			Controllable: strings.TrimSpace(file.Name) != "",
		})
	}
	return credentials, nil
}

// automationInventory reads and validates the live credential list for a read. It never
// writes the roster and never infers absence from a failure.
func (h *Handler) automationInventory(ctx context.Context) (automation.Inventory, error) {
	credentials, err := h.automationCredentials(ctx)
	if err != nil {
		return automation.Inventory{}, err
	}
	return automation.ValidateInventory(credentials)
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
	roster := []repository.AutomationAccount{}
	if h.automation != nil {
		listed, err := h.automation.ListAccounts(request.Context())
		if err != nil {
			writeInternalError(writer, err)
			return
		}
		roster = listed
	}
	inventory, inventoryErr := h.automationInventory(request.Context())
	plans := make(map[string]repository.AutomationAccount, len(roster))
	for _, account := range roster {
		if account.IsAdopted && account.RemovedAtMS == nil {
			plans[account.AuthIndex] = account
		}
	}
	// Every plan DTO is joined to its newest reading once; both the credential rows and
	// the roster share the projection.
	accountDTOs := h.automationAccountDTOs(request.Context(), roster)
	planDTOs := make(map[string]accountAutomationAccountDTO, len(accountDTOs))
	for _, dto := range accountDTOs {
		planDTOs[dto.AuthIndex] = dto
	}
	response := accountAutomationListResponse{
		Settings:  settings,
		Accounts:  accountDTOs,
		Inventory: h.automationInventoryDTO(inventory, inventoryErr, roster),
	}
	response.Credentials = automationCredentialDTOs(inventory, inventoryErr == nil, plans, planDTOs)
	if inventoryErr == nil {
		response.Candidates = automationCandidateDTOs(inventory.Ordered, plans)
	} else {
		response.Candidates = []accountAutomationCandidateDTO{}
	}
	response.AccountsLastKnown = inventoryErr != nil
	writeJSON(writer, http.StatusOK, response)
}

// automationCandidateDTOs projects the current credentials as adoption metadata: whether a
// plan already matched, and whether CPA can address them for a status patch.
func automationCandidateDTOs(credentials []automation.Credential, plans map[string]repository.AutomationAccount) []accountAutomationCandidateDTO {
	result := make([]accountAutomationCandidateDTO, 0, len(credentials))
	for _, credential := range credentials {
		candidate := accountAutomationCandidateDTO{
			AuthIndex:            credential.AuthIndex,
			Provider:             credential.Provider,
			Name:                 credential.Name,
			AccountLabel:         credential.AccountLabel,
			Disabled:             credential.Disabled,
			Controllable:         credential.Controllable,
			Adoptable:            credential.Controllable,
			AutoDisableSupported: automation.ProviderHasVerifiedWeeklyCapability(credential.Provider),
		}
		if _, ok := plans[credential.AuthIndex]; ok {
			candidate.Adopted = true
		}
		if !credential.Controllable {
			candidate.AdoptionBlockedReason = automation.ReasonCredentialMissingName
		}
		result = append(result, candidate)
	}
	return result
}

// automationInventoryDTO reports truthful counts. On a failed read every count is nil:
// an unavailable inventory is not an empty gateway.
func (h *Handler) automationInventoryDTO(inventory automation.Inventory, inventoryErr error, roster []repository.AutomationAccount) accountAutomationInventoryDTO {
	if inventoryErr != nil {
		// Nothing is "awaiting confirmation" when the inventory itself could not be read:
		// every count, including this one, is unknown during an outage.
		return accountAutomationInventoryDTO{Error: "credential list unavailable"}
	}
	pending := 0
	for _, account := range roster {
		if account.IsAdopted && account.RemovedAtMS == nil && !inventory.Present(account.AuthIndex) {
			pending++
		}
	}
	dto := accountAutomationInventoryDTO{PendingAbsence: &pending}
	total, managed, disabled := len(inventory.Ordered), 0, 0
	for _, credential := range inventory.Ordered {
		if credential.Disabled {
			disabled++
		}
		if account, ok := matchingPlan(credential.AuthIndex, roster); ok && account.IsAdopted && account.RemovedAtMS == nil {
			managed++
		}
	}
	unmanaged := total - managed
	dto.Available = true
	dto.Credentials = &total
	dto.Managed = &managed
	dto.Unmanaged = &unmanaged
	dto.Disabled = &disabled
	return dto
}

func matchingPlan(authIndex string, roster []repository.AutomationAccount) (repository.AutomationAccount, bool) {
	for _, account := range roster {
		if account.AuthIndex == authIndex {
			return account, true
		}
	}
	return repository.AutomationAccount{}, false
}

// automationCredentialDTOs projects the current inventory to the console's main table. The
// order is the gateway's own, so a duplicate display label can never merge two rows.
func automationCredentialDTOs(inventory automation.Inventory, available bool, plans map[string]repository.AutomationAccount, planDTOs map[string]accountAutomationAccountDTO) []accountAutomationCredentialDTO {
	result := make([]accountAutomationCredentialDTO, 0, len(inventory.Ordered))
	if !available {
		// A failed read returns no rows; the last-known plans travel in Accounts instead.
		return result
	}
	for _, credential := range inventory.Ordered {
		row := accountAutomationCredentialDTO{
			AuthIndex:            credential.AuthIndex,
			Provider:             credential.Provider,
			Name:                 credential.Name,
			AccountLabel:         credential.AccountLabel,
			Disabled:             credential.Disabled,
			Controllable:         credential.Controllable,
			Adoptable:            credential.Controllable,
			AutoDisableSupported: automation.ProviderHasVerifiedWeeklyCapability(credential.Provider),
		}
		if !credential.Controllable {
			row.AdoptionBlockedReason = automation.ReasonCredentialMissingName
		}
		if _, ok := plans[credential.AuthIndex]; ok {
			row.Managed = true
			if plan, ok := planDTOs[credential.AuthIndex]; ok {
				copied := plan
				row.Plan = &copied
			}
		}
		result = append(result, row)
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
			BurnCredit:           account.BurnCredit,
			Removed:              account.RemovedAtMS != nil,
			State:                account.State,
			MonitorReason:        account.MonitorReason,
			AutoDisableSupported: automation.ProviderHasVerifiedWeeklyCapability(account.Provider),
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
		if recovery, ok := automation.RecoveryDue(account, h.now()); ok {
			dueMS := recovery.AtMS
			dto.RecoveryAtMS = &dueMS
			dto.RecoveryStatus = string(recovery.Status)
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
		Enabled    *bool `json:"enabled"`
		BurnCredit *bool `json:"burn_credit"`
		// ExpectedIntentVersion lets a delayed or duplicated browser request name the
		// lifecycle it read. A plan adopted again after a removal has a different version,
		// so the stale request conflicts instead of changing the new incarnation's switch.
		ExpectedIntentVersion *int64 `json:"expected_intent_version"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if payload.Enabled == nil && payload.BurnCredit == nil {
		writeError(writer, http.StatusBadRequest, "enabled or burn_credit is required")
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
	updated, err := h.automation.SetAccountOptionsExpected(request.Context(), authIndex, payload.Enabled, payload.BurnCredit, payload.ExpectedIntentVersion)
	if err != nil {
		h.writeAccountAutomationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.automationAccountDTOs(request.Context(), []repository.AutomationAccount{updated})[0])
}

func (h *Handler) postAccountAutomationAdopt(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		AuthIndexes []string `json:"auth_indexes"`
		Enable      *bool    `json:"enable"`
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
	shouldEnable := payload.Enable == nil || *payload.Enable
	adopted, err := h.automation.Adopt(request.Context(), payload.AuthIndexes, shouldEnable, note)
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
	var payload struct {
		// ExpectedIntentVersion names the lifecycle the console read. A delayed or duplicated
		// cancel against a re-adopted incarnation conflicts instead of pausing the new one.
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
	// A cancel is local intent and must stay available during a CPA outage: it never reads
	// the gateway, so an unavailable inventory cannot block it.
	updated, err := h.automation.CancelAccountExpected(request.Context(), authIndex, payload.ExpectedIntentVersion)
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
		// ExpectedIntentVersion names the lifecycle the console read, so a delayed recovery
		// time cannot be scheduled onto a credential adopted again under the same index.
		ExpectedIntentVersion *int64 `json:"expected_intent_version"`
	}
	if err := decodeManagementJSON(writer, request, accountAutomationRequestLimit, &payload); err != nil {
		return
	}
	if payload.ExpectedIntentVersion != nil && *payload.ExpectedIntentVersion < 0 {
		writeError(writer, http.StatusBadRequest, "expected_intent_version must not be negative")
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
	updated, err := h.automation.SetManualResetExpected(request.Context(), authIndex, payload.ResetAtMS, payload.ExpectedIntentVersion)
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
	case errors.Is(err, automation.ErrStaleBatchMutation):
		writeError(writer, http.StatusConflict, "account state changed; reload and retry")
	case errors.Is(err, repository.ErrAutomationIdentityConflict):
		// Nothing reached the gateway: the current credential is not the one the old row
		// was adopted as. Unadopting it first is the operator's explicit fresh engagement.
		writeError(writer, http.StatusConflict, "credential identity changed; unadopt and adopt again")
	case err.Error() == "write_busy":
		writeError(writer, http.StatusConflict, "another write is in progress")
	default:
		writeCPAFacadeError(writer, err)
	}
}
