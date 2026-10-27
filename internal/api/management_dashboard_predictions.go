package api

import (
	"net/http"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/operations"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

type dashboardPredictionItem struct {
	operations.QuotaPredictionItem
	quota.PredictionAccounting
}

type dashboardPredictionProvider struct {
	Provider string                    `json:"provider"`
	Items    []dashboardPredictionItem `json:"items"`
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
	result, err := service.DashboardQuotaPrediction(ctx)
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
		provider := item.Provider
		if provider == "stepfun" {
			provider = "step"
		}
		index, ok := byProvider[provider]
		if !ok {
			index = len(response.Providers)
			byProvider[provider] = index
			response.Providers = append(response.Providers, dashboardPredictionProvider{
				Provider: provider,
				Items:    []dashboardPredictionItem{},
			})
		}
		response.Providers[index].Items = append(response.Providers[index].Items, dashboardPredictionItem{QuotaPredictionItem: item, PredictionAccounting: item.Accounting})
	}
	writeJSON(writer, http.StatusOK, response)
}
