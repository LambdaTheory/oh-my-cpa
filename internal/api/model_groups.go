package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Model group management (PRD §4.1-4.2, issue #100).
//
// These routes are session-authenticated and same-origin guarded through the same
// middleware as every other management write, and they are the only way a group
// changes: the console is the writer, and a later read-only agent surface will read
// what is stored here without being able to alter it. The DTOs are a handwritten
// allowlist over the repository shape so a column added there does not reach the wire
// until someone decides it should.
//
// Nothing here touches CPA. A group is OMC's own naming of model identifiers, so
// deleting one deletes rows in OMC's database and nothing in the gateway.

const modelGroupRequestLimit = 16 << 10

type modelGroupMemberDTO struct {
	Model     string `json:"model"`
	Position  int    `json:"position"`
	AddedAtMS int64  `json:"added_at_ms"`
}

type modelGroupDTO struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	IsPreset    bool                  `json:"is_preset"`
	CreatedAtMS int64                 `json:"created_at_ms"`
	UpdatedAtMS int64                 `json:"updated_at_ms"`
	Members     []modelGroupMemberDTO `json:"members"`
}

type modelGroupListDTO struct {
	Groups []modelGroupDTO `json:"groups"`
}

// modelGroupAddDTO answers an add request with what actually changed. `skipped` is not
// an error: the caller asked for the group to contain those models and it does, so
// saying which were already there is more useful than a bare success.
type modelGroupAddDTO struct {
	Group   modelGroupDTO `json:"group"`
	Added   []string      `json:"added"`
	Skipped []string      `json:"skipped"`
}

type modelGroupRestoreDTO struct {
	Created []modelGroupDTO `json:"created"`
	Skipped []string        `json:"skipped"`
}

func projectModelGroup(group repository.ModelGroup) modelGroupDTO {
	members := make([]modelGroupMemberDTO, 0, len(group.Members))
	for _, member := range group.Members {
		members = append(members, modelGroupMemberDTO{Model: member.Model, Position: member.Position, AddedAtMS: member.AddedAtMS})
	}
	return modelGroupDTO{
		ID:          group.ID,
		Name:        group.Name,
		IsPreset:    group.IsPreset,
		CreatedAtMS: group.CreatedAtMS,
		UpdatedAtMS: group.UpdatedAtMS,
		Members:     members,
	}
}

func writeModelGroupError(writer http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, ""
	switch {
	case errors.Is(err, repository.ErrModelGroupNotFound):
		status, code = http.StatusNotFound, "model_group_not_found"
	case errors.Is(err, repository.ErrModelGroupName):
		status, code = http.StatusBadRequest, "model_group_invalid_name"
	case errors.Is(err, repository.ErrModelGroupNameConflict):
		status, code = http.StatusConflict, "model_group_name_conflict"
	case errors.Is(err, repository.ErrModelGroupMember):
		status, code = http.StatusBadRequest, "model_group_invalid_member"
	case errors.Is(err, repository.ErrModelGroupOrder):
		status, code = http.StatusBadRequest, "model_group_invalid_order"
	case errors.Is(err, repository.ErrModelGroupAuditFailed):
		writeModelGroupAuditFailure(writer, err)
		return
	default:
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, status, map[string]string{"error": code, "code": code})
}

// modelGroupForWrite loads the group a request names, answering 404 itself when it is
// gone. Writes need the stored row (for the audit target and the error message), and
// every write path wants it before doing anything else.
func (h *Handler) modelGroupForWrite(writer http.ResponseWriter, request *http.Request) (repository.ModelGroup, bool) {
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "model group storage is unavailable")
		return repository.ModelGroup{}, false
	}
	group, err := h.repo.GetModelGroup(request.Context(), chi.URLParam(request, "id"))
	if err != nil {
		writeModelGroupError(writer, err)
		return repository.ModelGroup{}, false
	}
	return group, true
}

// modelGroupModelParam reads the member name from the path.
//
// It must not decode again. chi matches the escaped RawPath when there is one and the
// already-decoded Path otherwise, so by the time a handler sees the parameter the
// question is not "is this escaped" but "which form did the route match". The answer is
// the same test chi used: RawPath non-empty means the parameter is still escaped and
// needs one unescape; otherwise it is the decoded path segment and unescaping it again
// would turn a literal "a%2Fb" into "a/b" and delete the wrong member. An invalid escape
// that survives to here can only come from RawPath, where it is a literal, so it is kept
// as it is rather than refused.
func modelGroupModelParam(request *http.Request) (string, bool) {
	value := chi.URLParam(request, "model")
	if request.URL.RawPath != "" {
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

// modelGroupAudit assembles the event a repository write records in its own
// transaction. The request metadata (correlation id, masked source) is collected here
// because the repository has no request; everything else is decided by the write.
func (h *Handler) modelGroupAudit(request *http.Request, action string) repository.AuditEvent {
	details := map[string]any{}
	if clientID := clientRequestID(request); clientID != "" {
		details["client_request_id"] = clientID
	}
	event := repository.AuditEvent{
		Action:        action,
		TargetType:    "model_group",
		RequestID:     getOrGenerateRequestID(request),
		SourceSummary: requestSourceSummary(request),
	}
	if len(details) > 0 {
		event.Details = details
	}
	return event
}

// writeModelGroupAuditFailure answers a write whose trail could not be stored. The
// change was rolled back with it, so the stable code tells a caller this is an
// unavailable audit store rather than a rejected name or a failed write.
func writeModelGroupAuditFailure(writer http.ResponseWriter, err error) {
	slog.Error("model group write refused: the audit record could not be stored", "error", err.Error())
	writeAuditFailure(writer, "audit log failure; model group change aborted")
}

func (h *Handler) listModelGroups(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "model group storage is unavailable")
		return
	}
	groups, err := h.repo.ListModelGroups(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	response := modelGroupListDTO{Groups: make([]modelGroupDTO, 0, len(groups))}
	for _, group := range groups {
		response.Groups = append(response.Groups, projectModelGroup(group))
	}
	writeJSON(writer, http.StatusOK, response)
}

func (h *Handler) createModelGroup(writer http.ResponseWriter, request *http.Request) {
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "model group storage is unavailable")
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := decodeManagementJSON(writer, request, modelGroupRequestLimit, &payload); err != nil {
		return
	}
	group, err := h.repo.CreateModelGroup(request.Context(), payload.Name, h.now().UnixMilli(), h.modelGroupAudit(request, "model_group.create"))
	if err != nil {
		writeModelGroupError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, projectModelGroup(group))
}

func (h *Handler) renameModelGroup(writer http.ResponseWriter, request *http.Request) {
	existing, ok := h.modelGroupForWrite(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := decodeManagementJSON(writer, request, modelGroupRequestLimit, &payload); err != nil {
		return
	}
	group, err := h.repo.RenameModelGroup(request.Context(), existing.ID, payload.Name, h.now().UnixMilli(), h.modelGroupAudit(request, "model_group.rename"))
	if err != nil {
		writeModelGroupError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, projectModelGroup(group))
}

func (h *Handler) deleteModelGroup(writer http.ResponseWriter, request *http.Request) {
	existing, ok := h.modelGroupForWrite(writer, request)
	if !ok {
		return
	}
	removed, err := h.repo.DeleteModelGroup(request.Context(), existing.ID, h.now().UnixMilli(), h.modelGroupAudit(request, "model_group.delete"))
	if err != nil {
		writeModelGroupError(writer, err)
		return
	}
	if !removed {
		writeModelGroupError(writer, repository.ErrModelGroupNotFound)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) addModelGroupMembers(writer http.ResponseWriter, request *http.Request) {
	existing, ok := h.modelGroupForWrite(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Models []string `json:"models"`
	}
	if err := decodeManagementJSON(writer, request, modelGroupRequestLimit, &payload); err != nil {
		return
	}
	if len(payload.Models) == 0 {
		writeModelGroupError(writer, repository.ErrModelGroupMember)
		return
	}
	result, err := h.repo.AddModelGroupMembers(request.Context(), existing.ID, payload.Models, h.now().UnixMilli(), h.modelGroupAudit(request, "model_group.members.add"))
	if err != nil {
		writeModelGroupError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, modelGroupAddDTO{
		Group:   projectModelGroup(result.Group),
		Added:   result.Added,
		Skipped: result.Skipped,
	})
}

func (h *Handler) removeModelGroupMember(writer http.ResponseWriter, request *http.Request) {
	existing, ok := h.modelGroupForWrite(writer, request)
	if !ok {
		return
	}
	model, ok := modelGroupModelParam(request)
	if !ok {
		writeModelGroupError(writer, repository.ErrModelGroupMember)
		return
	}
	group, err := h.repo.RemoveModelGroupMember(request.Context(), existing.ID, model, h.now().UnixMilli(), h.modelGroupAudit(request, "model_group.members.remove"))
	if err != nil {
		writeModelGroupError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, projectModelGroup(group))
}

func (h *Handler) reorderModelGroupMembers(writer http.ResponseWriter, request *http.Request) {
	existing, ok := h.modelGroupForWrite(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Models []string `json:"models"`
	}
	if err := decodeManagementJSON(writer, request, modelGroupRequestLimit, &payload); err != nil {
		return
	}
	group, err := h.repo.ReorderModelGroupMembers(request.Context(), existing.ID, payload.Models, h.now().UnixMilli(), h.modelGroupAudit(request, "model_group.members.reorder"))
	if err != nil {
		writeModelGroupError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, projectModelGroup(group))
}

// restoreDefaultModelGroups recreates whichever preset groups are missing. It is
// idempotent: a group that still exists is reported as skipped and left untouched,
// members and order included, so calling it twice cannot duplicate anything.
func (h *Handler) restoreDefaultModelGroups(writer http.ResponseWriter, request *http.Request) {
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "model group storage is unavailable")
		return
	}
	result, err := h.repo.RestoreDefaultModelGroups(request.Context(), h.now().UnixMilli(), h.modelGroupAudit(request, "model_group.restore_defaults"))
	if err != nil {
		writeModelGroupError(writer, err)
		return
	}
	response := modelGroupRestoreDTO{Created: make([]modelGroupDTO, 0, len(result.Created)), Skipped: result.Skipped}
	for _, group := range result.Created {
		response.Created = append(response.Created, projectModelGroup(group))
	}
	writeJSON(writer, http.StatusOK, response)
}
