package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// repoKeyConsoleOperator is who the trail names for a decision taken in the console.
//
// The console is guarded by one shared management credential, so it identifies the
// deployment and not a person: writing an invented name here would be a fact the system
// does not have. The one-time approval link carries the same question and answers it the
// same way (PRD 4.9).
const repoKeyConsoleOperator = "console"

// RepoKeyBindingDTO is one repository's key as the console lists it.
//
// It carries the mask and never the key: the plaintext exists in the gateway, and the
// console reads it only through the deliberate, audited reveal below (PRD 4.9, AC-35).
type RepoKeyBindingDTO struct {
	ID            string `json:"id"`
	Repo          string `json:"repo"`
	RepoHasRemote bool   `json:"repo_has_remote"`
	// KeyFingerprint is the gateway's usage identity for this key: the same value the
	// client-key list carries, so the two lists can be matched up without either of them
	// holding the key itself (AC-14).
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	KeyMask        string `json:"key_mask"`
	// Alias is the operator's name for the key, if one was given. The gateway's own alias
	// list is keyed by fingerprint, so both lists show the same name (AC-15).
	Alias       string `json:"alias,omitempty"`
	Status      string `json:"status"`
	CreatedAtMS int64  `json:"created_at_ms"`
	CreatedBy   string `json:"created_by"`
	StoppedAtMS int64  `json:"stopped_at_ms,omitempty"`
	StoppedBy   string `json:"stopped_by"`
	StopCategory string `json:"stop_category"`
	StopReason   string `json:"stop_reason"`
	// LastRecipient and LastDeliveredAtMS are the newest hand-out, shown as 最近领取人 (AC-15).
	LastRecipient     string `json:"last_recipient,omitempty"`
	LastDeliveredAtMS int64  `json:"last_delivered_at_ms,omitempty"`
	// Usage7D is the last seven days of this key's observed traffic (AC-15).
	Usage7D RepoKeyUsageDTO `json:"usage_7d"`
}

// RepoKeyModelUsageDTO is one model's line in the detail's 按模型用量 (AC-18).
type RepoKeyModelUsageDTO struct {
	Model     string `json:"model"`
	Requests  int64  `json:"requests"`
	Failed    int64  `json:"failed"`
	Tokens    int64  `json:"tokens"`
	CostNanos int64  `json:"cost_nanos"`
}

// RepoKeyStoppedUsageDTO is the 停用后 block: only rejected requests are counted, and the
// cost is zero by definition (AC-13).
type RepoKeyStoppedUsageDTO struct {
	SinceMS         int64 `json:"since_ms"`
	Rejected        int64 `json:"rejected"`
	CostNanos       int64 `json:"cost_nanos"`
}

// RepoKeyUsageDTO is a key's usage in one window. Window is 24h, 7d or 30d (AC-19).
type RepoKeyUsageDTO struct {
	Window     string                `json:"window"`
	FromMS     int64                 `json:"from_ms"`
	ToMS       int64                 `json:"to_ms"`
	Requests   int64                 `json:"requests"`
	Failed     int64                 `json:"failed"`
	Tokens     int64                 `json:"total_tokens"`
	CostNanos  int64                 `json:"cost_nanos"`
	LastUsedMS int64                 `json:"last_used_ms"`
	Models     []RepoKeyModelUsageDTO `json:"models"`
	// StoppedAfter is present only for a stopped key: what it cost after it left service.
	StoppedAfter *RepoKeyStoppedUsageDTO `json:"stopped_after,omitempty"`
}

// RepoKeyApplicationDTO is one application as the queue shows it.
//
// public_key_fingerprint is the applicant's identity (AC-34); the public key itself, the
// envelope and the authorization token are not part of this view. The operator decides on
// who is asking and why, none of which is key material.
type RepoKeyApplicationDTO struct {
	ID                   string `json:"id"`
	Repo                 string `json:"repo"`
	RepoHasRemote        bool   `json:"repo_has_remote"`
	Agent                string `json:"agent"`
	Device               string `json:"device"`
	Purpose              string `json:"purpose"`
	Kind                 string `json:"kind"`
	Status               string `json:"status"`
	Source               string `json:"source"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	KeyMask              string `json:"key_mask"`
	KeyFingerprint       string `json:"key_fingerprint,omitempty"`
	DecidedBy            string `json:"decided_by"`
	DecidedAtMS          int64  `json:"decided_at_ms,omitempty"`
	RejectReason         string `json:"reject_reason"`
	// DecisionReason is what the approver wrote when approving (AC-21).
	DecisionReason     string `json:"decision_reason"`
	CiphertextReady    bool   `json:"ciphertext_ready"`
	FirstDeliveredAtMS int64  `json:"first_delivered_at_ms,omitempty"`
	CreatedAtMS        int64  `json:"created_at_ms"`
	// RequestKind is 'reenable' for a request to put a stopped key back in service; such a
	// request names the key it is about instead of carrying a public key (AC-31).
	RequestKind     string `json:"request_kind,omitempty"`
	TargetBindingID string `json:"target_binding_id,omitempty"`
	// ApprovalLink is the one-time link to this application (AC-8). It is only known while
	// the caller that filed it is being answered, so the queue leaves it empty.
	ApprovalLink string `json:"approval_link,omitempty"`
}

type RepoKeyDeliveryDTO struct {
	ID                   string `json:"id"`
	ApplicationID        string `json:"application_id"`
	Agent                string `json:"agent"`
	Device               string `json:"device"`
	Kind                 string `json:"kind"`
	Operator             string `json:"operator"`
	KeyMask              string `json:"key_mask"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	DeliveredAtMS        int64  `json:"delivered_at_ms"`
}

type RepoKeyStopDTO struct {
	ID             string `json:"id"`
	Category       string `json:"category"`
	Reason         string `json:"reason"`
	StoppedBy      string `json:"stopped_by"`
	StoppedAtMS    int64  `json:"stopped_at_ms"`
	ReenabledBy    string `json:"reenabled_by"`
	ReenabledAtMS  int64  `json:"reenabled_at_ms,omitempty"`
	ReenableReason string `json:"reenable_reason"`
}

func repoKeyBindingDTO(binding repository.RepoKeyBinding) RepoKeyBindingDTO {
	item := RepoKeyBindingDTO{
		ID: binding.ID, Repo: binding.RepoID, RepoHasRemote: binding.RepoHasRemote,
		KeyMask: binding.KeyMask, KeyFingerprint: binding.KeyFingerprint,
		Status: binding.Status, CreatedAtMS: binding.CreatedAtMS,
		CreatedBy: binding.CreatedBy, StoppedBy: binding.StoppedBy,
		StopCategory: binding.StopCategory, StopReason: binding.StopReason,
	}
	if binding.StoppedAtMS != nil {
		item.StoppedAtMS = *binding.StoppedAtMS
	}
	return item
}

func repoKeyApplicationDTO(application repository.RepoKeyApplication) RepoKeyApplicationDTO {
	item := RepoKeyApplicationDTO{
		ID: application.ID, Repo: application.RepoID, RepoHasRemote: application.RepoHasRemote,
		Agent: application.AgentName, Device: application.DeviceName, Purpose: application.Purpose,
		Kind: application.Kind, Status: application.Status, Source: application.Source,
		PublicKeyFingerprint: application.PublicKeyFingerprint, KeyMask: application.KeyMask,
		KeyFingerprint: application.KeyFingerprint,
		DecidedBy:      application.DecidedBy, RejectReason: application.RejectReason,
		DecisionReason:  application.DecisionReason,
		CiphertextReady: application.Status == "approved" && application.Ciphertext != "",
		CreatedAtMS:     application.CreatedAtMS,
		RequestKind:     application.RequestKind, TargetBindingID: application.TargetBindingID,
	}
	if application.DecidedAtMS != nil {
		item.DecidedAtMS = *application.DecidedAtMS
	}
	if application.FirstDeliveredAtMS != nil {
		item.FirstDeliveredAtMS = *application.FirstDeliveredAtMS
	}
	return item
}

func repoKeyDeliveryDTO(delivery repository.RepoKeyDelivery) RepoKeyDeliveryDTO {
	return RepoKeyDeliveryDTO{
		ID: delivery.ID, ApplicationID: delivery.ApplicationID, Agent: delivery.AgentName,
		Device: delivery.DeviceName, Kind: delivery.Kind, Operator: delivery.Operator,
		KeyMask: delivery.KeyMask, PublicKeyFingerprint: delivery.PublicKeyFingerprint,
		DeliveredAtMS: delivery.DeliveredAtMS,
	}
}

func repoKeyStopDTO(stop repository.RepoKeyStop) RepoKeyStopDTO {
	item := RepoKeyStopDTO{
		ID: stop.ID, Category: stop.Category, Reason: stop.Reason, StoppedBy: stop.StoppedBy,
		StoppedAtMS: stop.StoppedAtMS, ReenabledBy: stop.ReenabledBy,
		ReenableReason: stop.ReenableReason,
	}
	if stop.ReenabledAtMS != nil {
		item.ReenabledAtMS = *stop.ReenabledAtMS
	}
	return item
}

// writeRepoKeyError maps the service's failures onto the console's vocabulary.
//
// Repository key writes reach the gateway, so a failure can be CPA's answer as easily as
// OMC's own refusal; the CPA cases keep the facade's existing mapping so the console shows
// one description of a gateway problem whichever page hit it.
func writeRepoKeyError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrRepoKeyApplicationNotFound), errors.Is(err, repository.ErrRepoKeyNotFound):
		writeError(writer, http.StatusNotFound, "the repository key record was not found")
		return
	case errors.Is(err, repository.ErrRepoKeyConflict), errors.Is(err, repository.ErrRepoKeyApplicationPending),
		errors.Is(err, repository.ErrRepoKeyBindingNotReenable):
		writeError(writer, http.StatusConflict, err.Error())
		return
	case errors.Is(err, repository.ErrRepoKeyInvalid):
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, repository.ErrRepoKeyAuditFailed):
		writeError(writer, http.StatusInternalServerError, "audit log failure; nothing was changed")
		return
	}
	switch err.Error() {
	case "caller_identity_required", "invalid_public_key", "application_not_approved", "repo_key_mismatch":
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	case "repo_key_not_stopped":
		writeError(writer, http.StatusConflict, "only a stopped key can be re-enabled")
		return
	case "repo_key_not_reenable":
		writeError(writer, http.StatusConflict, "this key has no restorable copy; issue a new key for the repository")
		return
	case "repo_key_leaked_never_reenabled":
		writeError(writer, http.StatusConflict, "a key stopped as leaked is never re-enabled; issue a new one for the repository")
		return
	case "repo_key_already_in_gateway":
		writeError(writer, http.StatusConflict, "the key is already back in the gateway configuration")
		return
	case "repo_key_not_active":
		writeError(writer, http.StatusConflict, "the key is not in service")
		return
	case "repo_key_missing_in_gateway":
		writeError(writer, http.StatusConflict, "the key is no longer in the gateway configuration")
		return
	case "repo_key_sealed_unreadable", "cipher_unavailable":
		writeError(writer, http.StatusConflict, "the stored copy of this key cannot be opened; issue a new key for the repository")
		return
	case "approval_link_expired":
		writeError(writer, http.StatusGone, "this approval link has expired; the applicant can apply again for a new one")
		return
	}
	// A refusal that carries its own sentence (the service's invalid_parameters) says it
	// to the operator instead of handing back the code.
	if err.Error() == "invalid_parameters" {
		if detail := capability.ErrorDetail(err); detail != "" {
			writeError(writer, http.StatusBadRequest, detail)
			return
		}
	}
	writeCPAFacadeError(writer, err)
}

// listRepoKeyBindings answers the console's key table.
//
// It reports both the keys in service and the ones that were stopped, because a stop keeps
// every record (AC-12) and the operator needs to see what used to be in service. The search
// matches a repository, an agent name, the key's mask or its alias (AC-16); the status filter
// narrows to 使用中 or 已停用.
func (h *Handler) listRepoKeyBindings(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	ctx := request.Context()
	instanceID := defaultInstanceID()
	bindings, err := h.repo.ListRepoKeyBindings(ctx, instanceID)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	fingerprints := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		fingerprints = append(fingerprints, binding.KeyFingerprint)
	}
	aliases, err := h.repo.ClientKeyAliasesFor(ctx, instanceID, fingerprints)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	lastDeliveries, err := h.repo.ListRepoKeyLastDeliveries(ctx, instanceID)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	agentNames, err := h.repo.ListRepoKeyBindingAgentNames(ctx, instanceID)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	// The list's 近 7 天用量 is one read for every key, not one read per key (AC-15).
	_, fromMS, toMS := repoKeyUsageWindow(request, time.Now().UTC())
	sevenDay := map[string]repository.ClientKeyUsage{}
	if usage, usageErr := h.repo.ClientKeyUsage(ctx, instanceID, fromMS, toMS); usageErr == nil {
		for _, row := range usage {
			sevenDay[row.KeyFingerprint] = row
		}
	}
	query := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	items := make([]RepoKeyBindingDTO, 0, len(bindings))
	for _, binding := range bindings {
		if status != "" && status != "all" && binding.Status != status {
			continue
		}
		item := repoKeyBindingDTO(binding)
		item.Alias = aliases[binding.KeyFingerprint]
		if last, ok := lastDeliveries[binding.ID]; ok {
			item.LastRecipient = last.AgentName + " @ " + last.DeviceName
			item.LastDeliveredAtMS = last.DeliveredAtMS
		}
		if usage, ok := sevenDay[binding.KeyFingerprint]; ok {
			item.Usage7D = RepoKeyUsageDTO{
				Window: "7d", FromMS: fromMS, ToMS: toMS,
				Requests: usage.Requests, Failed: usage.Failed, Tokens: usage.TotalTokens,
				LastUsedMS: usage.LastUsedMS, Models: []RepoKeyModelUsageDTO{},
			}
		} else {
			item.Usage7D = RepoKeyUsageDTO{Window: "7d", FromMS: fromMS, ToMS: toMS, Models: []RepoKeyModelUsageDTO{}}
		}
		if query != "" && !repoKeyBindingMatches(query, binding, item.Alias, agentNames[binding.ID]) {
			continue
		}
		items = append(items, item)
	}
	pending, err := h.repo.CountRepoKeyApplications(ctx, instanceID, "pending")
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	awaiting, err := h.repo.CountRepoKeyAwaitingDelivery(ctx, instanceID)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"keys":                 items,
		"pending_applications": pending,
		"awaiting_delivery":    awaiting,
	})
}

// repoKeyBindingMatches is the console search (AC-16): the repository the key belongs to, the
// key's mask, the operator's alias, or an agent that applied for or received it.
func repoKeyBindingMatches(query string, binding repository.RepoKeyBinding, alias string, agents []string) bool {
	if strings.Contains(strings.ToLower(binding.RepoID), query) ||
		strings.Contains(strings.ToLower(binding.KeyMask), query) ||
		(alias != "" && strings.Contains(strings.ToLower(alias), query)) {
		return true
	}
	for _, agent := range agents {
		if strings.Contains(strings.ToLower(agent), query) {
			return true
		}
	}
	return false
}

// repoKeyUsageWindow reads the console's window switch (AC-19): 24h, 7d (default) or 30d.
func repoKeyUsageWindow(request *http.Request, now time.Time) (string, int64, int64) {
	preset := strings.TrimSpace(request.URL.Query().Get("window"))
	var span time.Duration
	switch preset {
	case "24h":
		span = 24 * time.Hour
	case "30d":
		span = 30 * 24 * time.Hour
	default:
		preset = "7d"
		span = 7 * 24 * time.Hour
	}
	toMS := now.UnixMilli()
	return preset, toMS - span.Milliseconds(), toMS
}

// listRepoKeyApplications answers the approval queue and the awaiting-delivery queue.
//
// `status` selects the queue; `awaiting=true` narrows an approved queue to the applications
// nobody has fetched yet, which is the "待交付" block the console shows next to the approvals.
func (h *Handler) listRepoKeyApplications(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	if status == "" {
		status = "pending"
	}
	switch status {
	case "pending", "approved", "rejected":
	default:
		writeError(writer, http.StatusBadRequest, "status must be pending, approved or rejected")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 200 {
			writeError(writer, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	awaitingOnly := strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("awaiting")), "true")

	var applications []repository.RepoKeyApplication
	var err error
	if waiting := awaitingOnly && status == "approved"; waiting {
		applications, err = h.repo.ListRepoKeyAwaitingDelivery(request.Context(), defaultInstanceID(), limit)
	} else {
		applications, err = h.repo.ListRepoKeyApplications(request.Context(), defaultInstanceID(), status, limit)
	}
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	items := make([]RepoKeyApplicationDTO, 0, len(applications))
	for _, application := range applications {
		items = append(items, repoKeyApplicationDTO(application))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"applications": items})
}

// getRepoKeyApplication answers one application's state for the queue's detail panel.
func (h *Handler) getRepoKeyApplication(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	application, err := h.repo.GetRepoKeyApplication(request.Context(), strings.TrimSpace(chi.URLParam(request, "id")))
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"application": repoKeyApplicationDTO(application)})
}

type repoKeyProxyApplyRequest struct {
	Repo                 string `json:"repo"`
	Purpose              string `json:"purpose"`
	PublicKey            string `json:"public_key"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	Agent                string `json:"agent"`
}

// createRepoKeyApprovalLink re-issues a pending application's one-time approval link for an
// operator to hand to the approver (AC-8).
//
// The stored record holds only the token's fingerprint, so the link the applicant received
// cannot be read back; a fresh one is issued and the earlier link stops working. The link is
// in this response only - nothing stores it, and the audit trail records that it happened
// without recording the token.
func (h *Handler) createRepoKeyApprovalLink(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	application, token, err := h.operationsService().IssueRepoKeyApprovalLink(request.Context(),
		strings.TrimSpace(chi.URLParam(request, "id")), repoKeyConsoleOperator)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"application":            repoKeyApplicationDTO(application),
		"approval_token":         token,
		"approval_url":           h.consoleURL(request) + "/repo-key-apply/" + token,
		"approval_expires_at_ms": application.TokenExpiresAtMS,
	})
}

// applyRepoKeyOnBehalf creates an application for a caller that cannot reach MCP yet.
//
// The console is a stand-in, not a shortcut: the application is queued like any other, and
// the console cannot approve something it just created without an operator pressing approve
// on the queue (AC-25). The device is recorded as the console so the trail does not claim an
// agent was on this machine when an operator typed its name.
func (h *Handler) applyRepoKeyOnBehalf(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyProxyApplyRequest
	if err := decodeManagementJSON(writer, request, 16*1024, &payload); err != nil {
		return
	}
	result, err := h.operationsService().ProxyRepoKeyApplication(
		// The browser reached the console at this address; the one-time link it gets back is
		// built from here so it opens on this deployment (AC-8).
		capability.WithConsoleURL(request.Context(), h.consoleURL(request)),
		payload.Repo, payload.Purpose, payload.PublicKey, payload.PublicKeyFingerprint,
		payload.Agent, repoKeyConsoleOperator)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"application_id":         result.ApplicationID,
		"status":                 result.Status,
		"kind":                   result.Kind,
		"approval_token":         result.ApprovalToken,
		"approval_url":           result.ApprovalURL,
		"approval_expires_at_ms": result.ApprovalExpires,
	})
}

// approveRepoKeyApplication is the console's half of the one decision both entry points take.
//
// Repeating it (a second tab, a stale page, the one-time link) reads the stored outcome
// instead of deciding again, because the service decides only while the row is pending. The
// approver may state a reason, which the approval record keeps (AC-21).
func (h *Handler) approveRepoKeyApplication(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyApproveRequest
	if err := decodeOptionalManagementJSON(writer, request, 8*1024, &payload); err != nil {
		return
	}
	id := strings.TrimSpace(chi.URLParam(request, "id"))
	application, err := h.operationsService().ApproveRepoKeyApplication(request.Context(), id, repoKeyConsoleOperator, payload.Reason)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"application": repoKeyApplicationDTO(application)})
}

type repoKeyApproveRequest struct {
	Reason string `json:"reason"`
}

type repoKeyRejectRequest struct {
	Reason string `json:"reason"`
}

// rejectRepoKeyApplication turns an application down. A reason is mandatory (AC-7): the
// caller is told why, and the trail keeps the sentence.
func (h *Handler) rejectRepoKeyApplication(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyRejectRequest
	if err := decodeManagementJSON(writer, request, 8*1024, &payload); err != nil {
		return
	}
	application, err := h.operationsService().RejectRepoKeyApplication(request.Context(),
		strings.TrimSpace(chi.URLParam(request, "id")), repoKeyConsoleOperator, payload.Reason)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"application": repoKeyApplicationDTO(application)})
}

// getRepoKeyBinding answers one key's detail: the binding, its stop history, every hand-out
// and the applications that named it, plus the usage this deployment observed for it (4.10).
func (h *Handler) getRepoKeyBinding(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	ctx := request.Context()
	binding, err := h.repo.GetRepoKeyBindingByID(ctx, strings.TrimSpace(chi.URLParam(request, "id")))
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	stops, err := h.repo.ListRepoKeyStops(ctx, defaultInstanceID(), binding.ID)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	deliveries, err := h.repo.ListRepoKeyDeliveries(ctx, defaultInstanceID(), binding.ID)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	applications, err := h.repo.ListRepoKeyApplicationsByBinding(ctx, defaultInstanceID(), binding.ID)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	stopItems := make([]RepoKeyStopDTO, 0, len(stops))
	for _, stop := range stops {
		stopItems = append(stopItems, repoKeyStopDTO(stop))
	}
	deliveryItems := make([]RepoKeyDeliveryDTO, 0, len(deliveries))
	for _, delivery := range deliveries {
		deliveryItems = append(deliveryItems, repoKeyDeliveryDTO(delivery))
	}
	applicationItems := make([]RepoKeyApplicationDTO, 0, len(applications))
	for _, application := range applications {
		applicationItems = append(applicationItems, repoKeyApplicationDTO(application))
	}
	item := repoKeyBindingDTO(binding)
	if aliases, aliasErr := h.repo.ClientKeyAliasesFor(ctx, defaultInstanceID(), []string{binding.KeyFingerprint}); aliasErr == nil {
		item.Alias = aliases[binding.KeyFingerprint]
	}
	if lastByBinding, lastErr := h.repo.ListRepoKeyLastDeliveries(ctx, defaultInstanceID()); lastErr == nil {
		if last, ok := lastByBinding[binding.ID]; ok {
			item.LastRecipient = last.AgentName + " @ " + last.DeviceName
			item.LastDeliveredAtMS = last.DeliveredAtMS
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"key":          item,
		"stops":        stopItems,
		"deliveries":   deliveryItems,
		"applications": applicationItems,
		"usage":        h.repoKeyUsage(ctx, request, binding),
	})
}

// repoKeyUsage reports what this deployment observed for one key in the window the operator
// selected (AC-18, AC-19): the summary, one line per model, and - for a stopped key - what it
// was asked after it left service (AC-13). It is OMC's own retained record, not a number read
// back from CPA, so a key nobody has used yet reports zeros rather than a guessed figure.
func (h *Handler) repoKeyUsage(ctx context.Context, request *http.Request, binding repository.RepoKeyBinding) RepoKeyUsageDTO {
	preset, fromMS, toMS := repoKeyUsageWindow(request, time.Now().UTC())
	usage := RepoKeyUsageDTO{
		Window: preset, FromMS: fromMS, ToMS: toMS,
		Models: []RepoKeyModelUsageDTO{},
	}
	filter := repository.UsageEventFilter{
		InstanceID: defaultInstanceID(), FromMS: fromMS, ToMS: toMS,
		APIGroupKeys: []string{binding.KeyFingerprint},
	}
	if aggregation, err := h.repo.AggregateUsage(ctx, filter, "all", 0); err == nil {
		usage.Requests = aggregation.Total.Requests
		usage.Failed = aggregation.Total.Failures
		usage.Tokens = aggregation.Total.Tokens
		usage.CostNanos = aggregation.Total.CostNanos
	}
	if aggregation, err := h.repo.AggregateUsage(ctx, filter, "model", 0); err == nil {
		for _, group := range aggregation.Groups {
			model := group.Key
			if strings.TrimSpace(model) == "" {
				model = "unknown"
			}
			usage.Models = append(usage.Models, RepoKeyModelUsageDTO{
				Model: model, Requests: group.Requests, Failed: group.Failures,
				Tokens: group.Tokens, CostNanos: group.CostNanos,
			})
		}
	}
	if keys, err := h.repo.ClientKeyUsage(ctx, defaultInstanceID(), fromMS, toMS); err == nil {
		for _, row := range keys {
			if row.KeyFingerprint == binding.KeyFingerprint {
				usage.LastUsedMS = row.LastUsedMS
				break
			}
		}
	}
	// 停用后: after the stop, every recorded request of this key was refused, and a refused
	// request costs nothing. The window starts at the stop or at the window's own start,
	// whichever is later (AC-13).
	if binding.Status == "stopped" && binding.StoppedAtMS != nil {
		sinceMS := *binding.StoppedAtMS
		if sinceMS < fromMS {
			sinceMS = fromMS
		}
		after := RepoKeyStoppedUsageDTO{SinceMS: sinceMS}
		failedFilter := repository.UsageEventFilter{
			InstanceID: defaultInstanceID(), FromMS: sinceMS, ToMS: toMS,
			APIGroupKeys: []string{binding.KeyFingerprint}, Result: repository.ResultFailed,
		}
		if aggregation, err := h.repo.AggregateUsage(ctx, failedFilter, "all", 0); err == nil {
			after.Rejected = aggregation.Total.Requests
		}
		usage.StoppedAfter = &after
	}
	return usage
}

type repoKeyStopRequest struct {
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

// stopRepoKeyBinding takes a key out of service. It removes the key from the gateway and
// keeps the record; a stopped key is never deleted (AC-10, AC-12).
func (h *Handler) stopRepoKeyBinding(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyStopRequest
	if err := decodeManagementJSON(writer, request, 8*1024, &payload); err != nil {
		return
	}
	binding, err := h.operationsService().StopRepoKeyBinding(request.Context(),
		strings.TrimSpace(chi.URLParam(request, "id")), repoKeyConsoleOperator, payload.Category, payload.Reason)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"key": repoKeyBindingDTO(binding)})
}

type repoKeyReenableRequest struct {
	Reason string `json:"reason"`
}

// requestRepoKeyReenable files a request to put a stopped key back in service.
//
// Re-enabling is a decision, not a button: the request waits in the approval queue and is
// approved by the same action as a key application, with a second confirmation and a reason
// (AC-31). No key value is accepted here — OMC never receives one (AC-35).
func (h *Handler) requestRepoKeyReenable(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyReenableRequest
	if err := decodeManagementJSON(writer, request, 8*1024, &payload); err != nil {
		return
	}
	application, err := h.operationsService().RequestRepoKeyReenable(request.Context(),
		strings.TrimSpace(chi.URLParam(request, "id")), repoKeyConsoleOperator, payload.Reason)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"application": repoKeyApplicationDTO(application)})
}

// RepoKeyRepoViewDTO is the repository's current state as an approver needs it (AC-4): which
// key is in service, who created it, who last received it, how it was used lately, and how it
// was stopped before. It carries masks and counts, never a key.
type RepoKeyRepoViewDTO struct {
	Repo                 string           `json:"repo"`
	RepoHasRemote        bool             `json:"repo_has_remote"`
	State                string           `json:"state"`
	KeyMask              string           `json:"key_mask,omitempty"`
	CreatedBy            string           `json:"created_by,omitempty"`
	CreatedAtMS          int64            `json:"created_at_ms,omitempty"`
	LastRecipient        string           `json:"last_recipient,omitempty"`
	LastDeliveredAtMS    int64            `json:"last_delivered_at_ms,omitempty"`
	Usage                RepoKeyUsageDTO `json:"usage"`
	Stops                []RepoKeyStopDTO `json:"stops"`
}

// RepoKeyReviewDTO is everything the approval page shows: what is being asked for, who is
// asking (with the fingerprint the applicant registered), the repository as it stands now,
// and whether that fingerprint changed since this caller last appeared (AC-4, AC-34).
type RepoKeyReviewDTO struct {
	Application                  RepoKeyApplicationDTO `json:"application"`
	Repo                         RepoKeyRepoViewDTO    `json:"repo"`
	PreviousPublicKeyFingerprint string                `json:"previous_public_key_fingerprint,omitempty"`
	PublicKeyChanged             bool                  `json:"public_key_changed"`
	Impact                       string                `json:"impact"`
	ApprovalExpired              bool                  `json:"approval_expired"`
}

// getRepoKeyBindingReview answers the approval page for one application. The one-time link and
// the queue show the same thing, so a person decides with the same information in both (AC-8).
func (h *Handler) getRepoKeyBindingReview(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	application, err := h.repo.GetRepoKeyApplication(request.Context(), strings.TrimSpace(chi.URLParam(request, "id")))
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.repoKeyReview(request.Context(), request, application, true))
}

func (h *Handler) repoKeyReview(ctx context.Context, request *http.Request, application repository.RepoKeyApplication, includeExpiry bool) RepoKeyReviewDTO {
	review := RepoKeyReviewDTO{
		Application: repoKeyApplicationDTO(application),
		Repo:        h.repoKeyRepoView(ctx, request, application.RepoID, application.RepoHasRemote),
	}
	// What approving would do, decided against the repository as it is now rather than the
	// state the applicant saw (AC-4).
	switch {
	case application.RequestKind == "reenable":
		review.Impact = "reenable"
	case review.Repo.State == "active":
		review.Impact = "reuse"
	case review.Repo.State == "stopped":
		review.Impact = "reissue"
	default:
		review.Impact = "issue"
	}
	if includeExpiry && application.Status == "pending" {
		review.ApprovalExpired = application.TokenExpiresAtMS > 0 && application.TokenExpiresAtMS < time.Now().UnixMilli()
	}
	// AC-34: the fingerprint this caller used before is shown next to the one it asks with, so
	// a changed machine or identity cannot pass as the same caller.
	if application.RequestKind != "reenable" && strings.TrimSpace(application.AgentName) != "" {
		if previous, err := h.repo.LatestRepoKeyApplicationForCaller(ctx, defaultInstanceID(), application.AgentName, application.DeviceName); err == nil {
			if previous.ID != application.ID && previous.PublicKeyFingerprint != "" && previous.PublicKeyFingerprint != application.PublicKeyFingerprint {
				review.PreviousPublicKeyFingerprint = previous.PublicKeyFingerprint
			}
		}
	}
	review.PublicKeyChanged = review.PreviousPublicKeyFingerprint != ""
	return review
}

// repoKeyRepoView reads the repository as it is now. A re-enable request is reviewed against
// its stopped key; anything else against whichever key the repository would hand out today.
func (h *Handler) repoKeyRepoView(ctx context.Context, request *http.Request, repoID string, repoHasRemote bool) RepoKeyRepoViewDTO {
	view := RepoKeyRepoViewDTO{Repo: repoID, RepoHasRemote: repoHasRemote, Stops: []RepoKeyStopDTO{}}
	status, err := h.repo.GetRepoKeyRepoStatus(ctx, defaultInstanceID(), repoID)
	if err == nil {
		view.State = status.State
		if status.Binding != nil {
			view.KeyMask = status.Binding.KeyMask
			view.CreatedBy = status.Binding.CreatedBy
			view.CreatedAtMS = status.Binding.CreatedAtMS
			view.Usage = h.repoKeyUsage(ctx, request, *status.Binding)
		}
		for _, stopBinding := range status.Stops {
			stops, stopErr := h.repo.ListRepoKeyStops(ctx, defaultInstanceID(), stopBinding.ID)
			if stopErr != nil {
				continue
			}
			for _, stop := range stops {
				view.Stops = append(view.Stops, repoKeyStopDTO(stop))
			}
		}
	}
	if view.Usage.Window == "" {
		view.Usage = RepoKeyUsageDTO{Window: "7d", Models: []RepoKeyModelUsageDTO{}}
	}
	if lastByBinding, lastErr := h.repo.ListRepoKeyLastDeliveries(ctx, defaultInstanceID()); lastErr == nil {
		bindings, bindErr := h.repo.ListRepoKeyBindings(ctx, defaultInstanceID())
		if bindErr == nil {
			for _, binding := range bindings {
				if binding.RepoID != repoID || binding.Status != "active" {
					continue
				}
				if last, ok := lastByBinding[binding.ID]; ok {
					view.LastRecipient = last.AgentName + " @ " + last.DeviceName
					view.LastDeliveredAtMS = last.DeliveredAtMS
				}
			}
		}
	}
	return view
}

// getRepoKeyRepoStatus answers the proxy application form's "what would happen" question
// (AC-29): the repository's current state and how a request filed now would be handled.
func (h *Handler) getRepoKeyRepoStatus(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	raw := strings.TrimSpace(request.URL.Query().Get("repo"))
	if raw == "" {
		writeError(writer, http.StatusBadRequest, "repo is required")
		return
	}
	repoID, hasRemote, err := repokey.NormalizeRepo(raw)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "the repo must be a remote identity such as org/repo, or a local directory name")
		return
	}
	view := h.repoKeyRepoView(request.Context(), request, repoID, hasRemote)
	writeJSON(writer, http.StatusOK, map[string]any{"repo": view})
}

// getRepoKeyApprovalLink resolves a one-time approval link. The token itself is never stored;
// its fingerprint is compared with the waiting application's (AC-8). The answer is the same
// review the queue shows, whether the application is still waiting or was already decided -
// one place deciding it makes the other stop offering it.
func (h *Handler) getRepoKeyApprovalLink(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	application, err := h.operationsService().RepoKeyApplicationByToken(request.Context(), chi.URLParam(request, "token"))
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, h.repoKeyReview(request.Context(), request, application, true))
}

// approveRepoKeyByLink is the same decision the queue posts, reached through the link.
func (h *Handler) approveRepoKeyByLink(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyApproveRequest
	if err := decodeOptionalManagementJSON(writer, request, 8*1024, &payload); err != nil {
		return
	}
	application, err := h.operationsService().RepoKeyApplicationByToken(request.Context(), chi.URLParam(request, "token"))
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	decided, err := h.operationsService().ApproveRepoKeyApplication(request.Context(), application.ID, repoKeyConsoleOperator, payload.Reason)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"application": repoKeyApplicationDTO(decided)})
}

// rejectRepoKeyByLink turns the linked application down, with a reason the applicant reads.
func (h *Handler) rejectRepoKeyByLink(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyRejectRequest
	if err := decodeManagementJSON(writer, request, 8*1024, &payload); err != nil {
		return
	}
	application, err := h.operationsService().RepoKeyApplicationByToken(request.Context(), chi.URLParam(request, "token"))
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	decided, err := h.operationsService().RejectRepoKeyApplication(request.Context(), application.ID, repoKeyConsoleOperator, payload.Reason)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"application": repoKeyApplicationDTO(decided)})
}

// fetchRepoKeyCiphertext is the console's 取密文: it hands the stored envelope to the operator
// so it can reach the caller, records the hand-out, and the application leaves the 待交付
// queue (AC-33). It never returns a plaintext.
func (h *Handler) fetchRepoKeyCiphertext(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	result, err := h.operationsService().FetchRepoKeyCiphertext(request.Context(),
		strings.TrimSpace(chi.URLParam(request, "id")), repoKeyConsoleOperator)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"application_id": result.ApplicationID,
		"kind":           result.Kind,
		"key_mask":       result.KeyMask,
		"algorithm":      result.Algorithm,
		"ciphertext":     result.Ciphertext,
		"instructions":   result.Instructions,
	})
}

// getRepoKeyAuditPush reports the audit platform link's health: whether it is configured, how
// many events wait, how many were quarantined, and when the last one got through (AC-32).
func (h *Handler) getRepoKeyAuditPush(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	status, err := h.repo.AuditPushStatusOf(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"enabled":         strings.TrimSpace(h.cfg.AuditPush.URL) != "" && !h.cfg.IsDemoMode,
		"pending":         status.Pending,
		"quarantined":     status.Quarantined,
		"earliest_ms":     status.EarliestMS,
		"last_success_ms": status.LastSuccessMS,
	})
}

type repoKeyRevealRequest struct {
	Reason string `json:"reason"`
}

// revealRepoKeyPlaintext is the console's last resort: the one place a key's plaintext is
// shown, on an explicit request with a stated reason, and audited on its own (PRD 4.9).
//
// The response never touches the browser's ordinary read cache, and the value is not echoed
// into any other view.
func (h *Handler) revealRepoKeyPlaintext(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyRevealRequest
	if err := decodeManagementJSON(writer, request, 8*1024, &payload); err != nil {
		return
	}
	plaintext, err := h.operationsService().RevealRepoKeyPlaintext(request.Context(),
		strings.TrimSpace(chi.URLParam(request, "id")), repoKeyConsoleOperator, payload.Reason)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"key": plaintext})
}
