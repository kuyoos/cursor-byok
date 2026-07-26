package config

import (
	"context"
	"strings"

	legacyruntime "cursor/internal/runtime"
)

const (
	defaultChannelTimeoutMS           = int((2 * 60 * 60) * 1000)
	defaultChannelContextWindowTokens = 200_000
	defaultChannelMaxTokens           = 65_536
	defaultChannelThinkingBudget      = 4_096
	defaultChannelAnthropicEffort     = "xhigh"
)

func (manager *Manager) SelectChannelForModel(_ context.Context, modelID string) (*legacyruntime.ResolvedChannel, error) {
	if manager == nil {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	adapters, err := NormalizeModelAdapterConfigs(manager.Current().ModelAdapters)
	if err != nil {
		return nil, err
	}
	return resolveModelAdapterChannel(adapters, modelID)
}

func resolveProjectedAdapterIndex(adapters []ModelAdapterConfig, requestedModel string) (int, bool) {
	target := strings.TrimSpace(requestedModel)
	if target == "" || target == "auto" || target == "fast" || target == "default" {
		if len(adapters) == 0 {
			return -1, false
		}
		return 0, true
	}
	for index, adapter := range adapters {
		if strings.TrimSpace(adapter.ID) == target {
			return index, true
		}
	}
	legacyMatch := -1
	for index, adapter := range adapters {
		for _, legacyID := range adapter.LegacyChannelIDs {
			if strings.TrimSpace(legacyID) != target {
				continue
			}
			if legacyMatch >= 0 && legacyMatch != index {
				return -1, false
			}
			legacyMatch = index
		}
	}
	if legacyMatch >= 0 {
		return legacyMatch, true
	}
	modelMatch := -1
	for index, adapter := range adapters {
		if strings.TrimSpace(adapter.ModelID) != target {
			continue
		}
		if modelMatch >= 0 {
			return -1, false
		}
		modelMatch = index
	}
	return modelMatch, modelMatch >= 0
}

func resolveModelAdapterChannel(adapters []ModelAdapterConfig, requestedModel string) (*legacyruntime.ResolvedChannel, error) {
	matchIndex, ok := resolveProjectedAdapterIndex(adapters, requestedModel)
	if !ok {
		return nil, legacyruntime.ErrChannelNotAvailable
	}
	matched := adapters[matchIndex]

	resolved := &legacyruntime.ResolvedChannel{
		ID:                          strings.TrimSpace(matched.ID),
		ProviderID:                  strings.TrimSpace(matched.ProviderID),
		ModelConfigID:               strings.TrimSpace(matched.ModelConfigID),
		Name:                        strings.TrimSpace(matched.DisplayName),
		GroupName:                   "local",
		Code:                        strings.TrimSpace(matched.ID),
		Provider:                    strings.TrimSpace(matched.Type),
		BaseURL:                     strings.TrimSpace(matched.BaseURL),
		APIKey:                      strings.TrimSpace(matched.APIKey),
		Model:                       strings.TrimSpace(matched.ModelID),
		OpenAIEndpoint:              strings.TrimSpace(matched.OpenAIEndpoint),
		OpenAIExtraParamsEnabled:    matched.OpenAIExtraParamsEnabled,
		OpenAIExtraParamsJSON:       strings.TrimSpace(matched.OpenAIExtraParamsJSON),
		CustomHeadersEnabled:        matched.CustomHeadersEnabled,
		CustomHeadersJSON:           strings.TrimSpace(matched.CustomHeadersJSON),
		AnthropicExtraParamsEnabled: matched.AnthropicExtraParamsEnabled,
		AnthropicExtraParamsJSON:    strings.TrimSpace(matched.AnthropicExtraParamsJSON),
		TimeoutMS:                   defaultChannelTimeoutMS,
		ContextWindowTokens:         defaultChannelContextWindowTokens,
		MaxTokens:                   defaultChannelMaxTokens,
		ReasoningEffort:             strings.TrimSpace(matched.ReasoningEffort),
		AnthropicMaxTokens:          defaultChannelMaxTokens,
		AnthropicThinkingEffort:     defaultChannelAnthropicEffort,
		ThinkingEnabled:             true,
		ThinkingBudgetTokens:        defaultChannelThinkingBudget,
	}
	if matched.ContextWindowTokens > 0 {
		resolved.ContextWindowTokens = matched.ContextWindowTokens
	}
	if matched.MaxCompletionTokens > 0 {
		resolved.MaxTokens = matched.MaxCompletionTokens
	}
	if matched.AnthropicMaxTokens > 0 {
		resolved.AnthropicMaxTokens = matched.AnthropicMaxTokens
	}
	if matched.ThinkingBudgetTokens > 0 {
		resolved.ThinkingBudgetTokens = matched.ThinkingBudgetTokens
	}
	if strings.TrimSpace(matched.AnthropicThinkingEffort) != "" {
		resolved.AnthropicThinkingEffort = strings.TrimSpace(matched.AnthropicThinkingEffort)
	}
	return resolved, nil
}
