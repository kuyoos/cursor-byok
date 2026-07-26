package historymetrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
		ByProvider:         byProvider,
		ByProviderModel:    byProviderModel,
	}, nil
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
