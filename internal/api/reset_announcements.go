package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type resetAnnouncementWriteDTO struct {
	Provider          string `json:"provider"`
	Source            string `json:"source"`
	ExpectedResetAtMS *int64 `json:"expected_reset_at_ms"`
	Scope             string `json:"scope"`
	Model             string `json:"model"`
	AuthIndex         string `json:"auth_index"`
	Confidence        string `json:"confidence"`
	OriginalText      string `json:"original_text"`
}
type resetAnnouncementDTO struct {
	resetAnnouncementWriteDTO
	ID             string `json:"id"`
	RegisteredAtMS int64  `json:"registered_at_ms"`
	CreatedAtMS    int64  `json:"created_at_ms"`
	Status         string `json:"status"`
}
type resetAnnouncementListDTO struct {
	Items []resetAnnouncementDTO `json:"items"`
	Total int                    `json:"total"`
}

func projectResetAnnouncement(item repository.ResetAnnouncement) resetAnnouncementDTO {
	return resetAnnouncementDTO{resetAnnouncementWriteDTO: resetAnnouncementWriteDTO{Provider: item.Provider, Source: item.Source, ExpectedResetAtMS: item.ExpectedResetAtMS, Scope: item.Scope, Model: item.Model, AuthIndex: item.AuthIndex, Confidence: item.Confidence, OriginalText: item.OriginalText}, ID: item.ID, RegisteredAtMS: item.RegisteredAtMS, CreatedAtMS: item.CreatedAtMS, Status: item.Status}
}
func writeAnnouncementError(writer http.ResponseWriter, err error) {
	if errors.Is(err, repository.ErrInvalidResetAnnouncement) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid_reset_announcement", "code": "invalid_reset_announcement"})
		return
	}
	writeInternalError(writer, err)
}
func (h *Handler) createResetAnnouncement(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	var input resetAnnouncementWriteDTO
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		writeAnnouncementError(writer, repository.ErrInvalidResetAnnouncement)
		return
	}
	item, err := h.repo.CreateResetAnnouncement(request.Context(), repository.ResetAnnouncementInput{Provider: input.Provider, Source: input.Source, ExpectedResetAtMS: input.ExpectedResetAtMS, Scope: input.Scope, Model: input.Model, AuthIndex: input.AuthIndex, Confidence: input.Confidence, OriginalText: input.OriginalText}, h.now().UnixMilli())
	if err != nil {
		writeAnnouncementError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, projectResetAnnouncement(item))
}
func (h *Handler) listResetAnnouncements(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	filter := repository.ResetAnnouncementFilter{Status: request.URL.Query().Get("status"), Limit: 20}
	for name, target := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		if value, ok := request.URL.Query()[name]; ok {
			parsed, err := strconv.Atoi(value[0])
			if err != nil {
				writeAnnouncementError(writer, repository.ErrInvalidResetAnnouncement)
				return
			}
			*target = parsed
		}
	}
	items, total, err := h.repo.ListResetAnnouncements(request.Context(), filter, h.now().UnixMilli())
	if err != nil {
		writeAnnouncementError(writer, err)
		return
	}
	response := resetAnnouncementListDTO{Items: []resetAnnouncementDTO{}, Total: total}
	for _, item := range items {
		response.Items = append(response.Items, projectResetAnnouncement(item))
	}
	writeJSON(writer, http.StatusOK, response)
}
