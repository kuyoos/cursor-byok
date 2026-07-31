package historymetrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type usageFileAttribution struct {
	ProviderID       string `json:"provider_id"`
	ModelConfigID    string `json:"model_config_id"`
	Model            string `json:"model"`
	ProviderCalls    int64  `json:"provider_calls"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

type usageFileDaily struct {
	Date              string `json:"date"`
	ProviderCalls     int64  `json:"provider_calls"`
	TurnsTotal        int64  `json:"turns_total"`
	ValidTurnsTotal   int64  `json:"valid_turns_total"`
	InvalidTurnsTotal int64  `json:"invalid_turns_total"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	CacheReadTokens   int64  `json:"cache_read_tokens"`
	CacheWriteTokens  int64  `json:"cache_write_tokens"`
	TotalTokens       int64  `json:"total_tokens"`
}

type usageFileEvent struct {
	EventID          string    `json:"event_id"`
	Kind             string    `json:"kind,omitempty"`
	Status           string    `json:"status,omitempty"`
	ProviderID       string    `json:"provider_id,omitempty"`
	ModelConfigID    string    `json:"model_config_id,omitempty"`
	Model            string    `json:"model,omitempty"`
	At               time.Time `json:"at"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	UsagePresent     bool      `json:"usage_present"`
}

type usageFileDocument struct {
	Totals struct {
		ProviderCalls     int64 `json:"provider_calls"`
		TurnsTotal        int64 `json:"turns_total"`
		ValidTurnsTotal   int64 `json:"valid_turns_total"`
		InvalidTurnsTotal int64 `json:"invalid_turns_total"`
		InputTokens       int64 `json:"input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
		CacheReadTokens   int64 `json:"cache_read_tokens"`
		CacheWriteTokens  int64 `json:"cache_write_tokens"`
		TotalTokens       int64 `json:"total_tokens"`
	} `json:"totals"`
	ByProvider      map[string]usageFileAttribution `json:"by_provider"`
	ByProviderModel map[string]usageFileAttribution `json:"by_provider_model"`
	Daily           []usageFileDaily                `json:"daily"`
	RecentEvents    []usageFileEvent                `json:"recent_events"`
}

func LoadUsageSummary(path string) (Summary, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Summary{}, nil
		}
		return Summary{}, fmt.Errorf("read usage file: %w", err)
	}
	var doc usageFileDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return Summary{}, fmt.Errorf("decode usage file: %w", err)
	}
	totals := Totals{
		InputTokens:        doc.Totals.InputTokens,
		OutputTokens:       doc.Totals.OutputTokens,
		CacheReadTokens:    doc.Totals.CacheReadTokens,
		CacheWriteTokens:   doc.Totals.CacheWriteTokens,
		PromptTokensTotal:  doc.Totals.InputTokens + doc.Totals.CacheReadTokens + doc.Totals.CacheWriteTokens,
		RequestTokensTotal: doc.Totals.TotalTokens,
	}
	byProvider := make(map[string]Attribution, len(doc.ByProvider))
	for key, item := range doc.ByProvider {
		byProvider[key] = toAttribution(item)
	}
	byProviderModel := make(map[string]Attribution, len(doc.ByProviderModel))
	for key, item := range doc.ByProviderModel {
		byProviderModel[key] = toAttribution(item)
	}
	return Summary{
		ProviderCallsTotal: int(doc.Totals.ProviderCalls),
		TurnsTotal:         int(doc.Totals.TurnsTotal),
		ValidTurnsTotal:    int(doc.Totals.ValidTurnsTotal),
		InvalidTurnsTotal:  int(doc.Totals.InvalidTurnsTotal),
		RequestTokensTotal: totals.RequestTokensTotal,
		PromptTokensTotal:  totals.PromptTokensTotal,
		CacheReadTokens:    totals.CacheReadTokens,
		CacheWriteTokens:   totals.CacheWriteTokens,
		CacheHitRate:       cacheHitRateFromTotals(totals),
		Daily:              toDaily(doc.Daily),
		RecentEvents:       toUsageEvents(doc.RecentEvents),
		ByProvider:         byProvider,
		ByProviderModel:    byProviderModel,
	}, nil
}

func toDaily(items []usageFileDaily) []Daily {
	result := make([]Daily, 0, len(items))
	for _, item := range items {
		result = append(result, Daily{
			Date:              item.Date,
			ProviderCalls:     item.ProviderCalls,
			TurnsTotal:        item.TurnsTotal,
			ValidTurnsTotal:   item.ValidTurnsTotal,
			InvalidTurnsTotal: item.InvalidTurnsTotal,
			InputTokens:       item.InputTokens,
			OutputTokens:      item.OutputTokens,
			CacheReadTokens:   item.CacheReadTokens,
			CacheWriteTokens:  item.CacheWriteTokens,
			TotalTokens:       item.TotalTokens,
		})
	}
	return result
}

func toUsageEvents(items []usageFileEvent) []UsageEvent {
	result := make([]UsageEvent, 0, len(items))
	for _, item := range items {
		result = append(result, UsageEvent{
			EventID:          item.EventID,
			Kind:             item.Kind,
			Status:           item.Status,
			ProviderID:       item.ProviderID,
			ModelConfigID:    item.ModelConfigID,
			Model:            item.Model,
			At:               formatUsageEventTime(item.At),
			InputTokens:      item.InputTokens,
			OutputTokens:     item.OutputTokens,
			CacheReadTokens:  item.CacheReadTokens,
			CacheWriteTokens: item.CacheWriteTokens,
			TotalTokens:      item.TotalTokens,
			UsagePresent:     item.UsagePresent,
		})
	}
	return result
}

func formatUsageEventTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func toAttribution(item usageFileAttribution) Attribution {
	return Attribution{
		ProviderID:       item.ProviderID,
		ModelConfigID:    item.ModelConfigID,
		Model:            item.Model,
		ProviderCalls:    item.ProviderCalls,
		InputTokens:      item.InputTokens,
		OutputTokens:     item.OutputTokens,
		CacheReadTokens:  item.CacheReadTokens,
		CacheWriteTokens: item.CacheWriteTokens,
		TotalTokens:      item.TotalTokens,
	}
}
