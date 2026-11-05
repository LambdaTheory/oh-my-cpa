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
	KeyMask       string `json:"key_mask"`
	Status        string `json:"status"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	CreatedBy     string `json:"created_by"`
	StoppedAtMS   int64  `json:"stopped_at_ms,omitempty"`
	StoppedBy     string `json:"stopped_by"`
	StopCategory  string `json:"stop_category"`
	StopReason    string `json:"stop_reason"`
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
	DecidedBy            string `json:"decided_by"`
	DecidedAtMS          int64  `json:"decided_at_ms,omitempty"`
	RejectReason         string `json:"reject_reason"`
	CiphertextReady      bool   `json:"ciphertext_ready"`
	FirstDeliveredAtMS   int64  `json:"first_delivered_at_ms,omitempty"`
	CreatedAtMS          int64  `json:"created_at_ms"`
}

type RepoKeyDeliveryDTO struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	Agent         string `json:"agent"`
	Device        string `json:"device"`
	Kind          string `json:"kind"`
	Operator      string `json:"operator"`
	DeliveredAtMS int64  `json:"delivered_at_ms"`
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
		KeyMask: binding.KeyMask, Status: binding.Status, CreatedAtMS: binding.CreatedAtMS,
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
		DecidedBy: application.DecidedBy, RejectReason: application.RejectReason,
		CiphertextReady: application.Status == "approved" && application.Ciphertext != "",
		CreatedAtMS:     application.CreatedAtMS,
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
// is a repository-name substring: the table is small and the names are what an operator
// remembers.
func (h *Handler) listRepoKeyBindings(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	bindings, err := h.repo.ListRepoKeyBindings(request.Context(), defaultInstanceID())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	query := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	items := make([]RepoKeyBindingDTO, 0, len(bindings))
	for _, binding := range bindings {
		if query != "" && !strings.Contains(strings.ToLower(binding.RepoID), query) {
			continue
		}
		if status != "" && status != "all" && binding.Status != status {
			continue
		}
		items = append(items, repoKeyBindingDTO(binding))
	}
	pending, err := h.repo.CountRepoKeyApplications(request.Context(), defaultInstanceID(), "pending")
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	awaiting, err := h.repo.CountRepoKeyAwaitingDelivery(request.Context(), defaultInstanceID())
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
	result, err := h.operationsService().ProxyRepoKeyApplication(request.Context(),
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
// instead of deciding again, because the service decides only while the row is pending.
func (h *Handler) approveRepoKeyApplication(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	id := strings.TrimSpace(chi.URLParam(request, "id"))
	application, err := h.operationsService().ApproveRepoKeyApplication(request.Context(), id, repoKeyConsoleOperator)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"application": repoKeyApplicationDTO(application)})
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
	writeJSON(writer, http.StatusOK, map[string]any{
		"key":          repoKeyBindingDTO(binding),
		"stops":        stopItems,
		"deliveries":   deliveryItems,
		"applications": applicationItems,
		"usage":        h.repoKeyUsage(ctx, request, binding.KeyFingerprint),
	})
}

// repoKeyUsage reports what this deployment observed for one key in the console's default
// window. It is OMC's own retained record, not a number read back from CPA, so a key nobody
// has used yet reports zeros rather than a guessed figure.
func (h *Handler) repoKeyUsage(ctx context.Context, request *http.Request, fingerprint string) repository.ClientKeyUsage {
	usage := repository.ClientKeyUsage{KeyFingerprint: fingerprint}
	window, windowErr := dashboardWindowFromRequest(request, time.Now().UTC())
	if windowErr != "" {
		return usage
	}
	all, err := h.repo.ClientKeyUsage(ctx, defaultInstanceID(), window.FromMS, window.ToMS)
	if err != nil {
		return usage
	}
	for _, row := range all {
		if row.KeyFingerprint == fingerprint {
			return row
		}
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
	Key    string `json:"key"`
}

// reenableRepoKeyBinding puts a stopped key back into service.
//
// Oh My CPA keeps no plaintext (AC-35), so the operator supplies the key again and the
// service checks it against the stop record's fingerprint before writing it back: the
// restored key is the same key, not a new one (AC-31).
func (h *Handler) reenableRepoKeyBinding(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	var payload repoKeyReenableRequest
	if err := decodeManagementJSON(writer, request, 16*1024, &payload); err != nil {
		return
	}
	binding, err := h.operationsService().ReenableRepoKeyBinding(request.Context(),
		strings.TrimSpace(chi.URLParam(request, "id")), repoKeyConsoleOperator, payload.Reason, payload.Key)
	if err != nil {
		writeRepoKeyError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"key": repoKeyBindingDTO(binding)})
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
