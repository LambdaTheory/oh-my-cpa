package api

import (
	"net/http"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/operations"
)

type dashboardPredictionProvider struct {
	Provider string                           `json:"provider"`
	Items    []operations.QuotaPredictionItem `json:"items"`
}

type dashboardPredictionsResponse struct {
	GeneratedAtMS int64                         `json:"generated_at_ms"`
	DurationMS    int64                         `json:"duration_ms"`
	Omitted       int                           `json:"omitted"`
	Providers     []dashboardPredictionProvider `json:"providers"`
}

// dashboardPredictions projects only OMC's stored observations. The ordinary
// console context is intentionally not marked read-only: the authenticated
// operator needs the credential label and auth index that MCP withholds.
func (h *Handler) dashboardPredictions(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	ctx := capability.WithAnchor(request.Context(), h.now().UnixMilli())
	service := h.operationsService()
	started := time.Now()
	result, err := service.QuotaPrediction(
		ctx,
		operations.QuotaPredictionInput{FindingsOnly: false},
	)
	durationMS := time.Since(started).Milliseconds()
	if err != nil {
		writeInternalError(writer, err)
		return
	}

	response := dashboardPredictionsResponse{
		GeneratedAtMS: result.GeneratedAtMS,
		DurationMS:    durationMS,
		Omitted:       result.Omitted,
		Providers:     []dashboardPredictionProvider{},
	}
	byProvider := make(map[string]int)
	for _, item := range result.Items {
		index, ok := byProvider[item.Provider]
		if !ok {
			index = len(response.Providers)
			byProvider[item.Provider] = index
			response.Providers = append(response.Providers, dashboardPredictionProvider{
				Provider: item.Provider,
				Items:    []operations.QuotaPredictionItem{},
			})
		}
		response.Providers[index].Items = append(response.Providers[index].Items, item)
	}
	writeJSON(writer, http.StatusOK, response)
}
