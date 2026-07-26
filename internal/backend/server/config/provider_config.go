package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"cursor/internal/modelchannel"
)

func NormalizeProviderConfigs(input []ProviderConfig) ([]ProviderConfig, error) {
	if len(input) == 0 {
		return []ProviderConfig{}, nil
	}
	providers := make([]ProviderConfig, 0, len(input))
	seenProviderIDs := make(map[string]struct{}, len(input))
	for providerIndex, source := range input {
		baseURL, err := modelchannel.NormalizeBaseURL(source.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("中转站 %d: %w", providerIndex+1, err)
		}
		providerType := normalizeModelAdapterType(source.Type)
		provider := ProviderConfig{
			ID:                   strings.TrimSpace(source.ID),
			Name:                 strings.TrimSpace(source.Name),
			Type:                 providerType,
			BaseURL:              baseURL,
			APIKey:               strings.TrimSpace(source.APIKey),
			DiscoveryPath:        normalizeDiscoveryPath(source.DiscoveryPath),
			CustomHeadersEnabled: source.CustomHeadersEnabled,
			CustomHeadersJSON:    strings.TrimSpace(source.CustomHeadersJSON),
			Models:               []ProviderModelConfig{},
		}
		if provider.ID == "" {
			provider.ID = stableConfigID("provider", strings.Join([]string{provider.Type, provider.BaseURL, provider.APIKey, provider.Name}, "\n"))
		}
		switch {
		case provider.Name == "":
			return nil, fmt.Errorf("中转站 %d 的名称不能为空", providerIndex+1)
		case provider.Type == "":
			return nil, fmt.Errorf("中转站 %d 的类型仅支持 openai 或 anthropic", providerIndex+1)
		case provider.APIKey == "":
			return nil, fmt.Errorf("中转站 %d 的 apiKey 不能为空", providerIndex+1)
		case provider.CustomHeadersEnabled:
			if err := validateHeadersJSON(provider.CustomHeadersJSON); err != nil {
				return nil, fmt.Errorf("中转站 %d: %w", providerIndex+1, err)
			}
		}
		if _, exists := seenProviderIDs[provider.ID]; exists {
			return nil, fmt.Errorf("中转站 ID 重复: %s", provider.ID)
		}
		seenProviderIDs[provider.ID] = struct{}{}

		seenModels := make(map[string]struct{}, 1)
		seenModelIDs := make(map[string]struct{}, 1)
		for modelIndex, modelSource := range selectProviderModels(source.Models) {
			model, err := normalizeProviderModel(provider, modelSource)
			if err != nil {
				return nil, fmt.Errorf("中转站 %s 的模型 %d: %w", provider.Name, modelIndex+1, err)
			}
			modelKey := strings.ToLower(model.ModelID)
			if _, exists := seenModels[modelKey]; exists {
				return nil, fmt.Errorf("中转站 %s 的 modelID 重复: %s", provider.Name, model.ModelID)
			}
			if _, exists := seenModelIDs[model.ID]; exists {
				return nil, fmt.Errorf("中转站 %s 的模型 ID 重复: %s", provider.Name, model.ID)
			}
			seenModels[modelKey] = struct{}{}
			seenModelIDs[model.ID] = struct{}{}
			provider.Models = append(provider.Models, model)
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func selectProviderModels(models []ProviderModelConfig) []ProviderModelConfig {
	if len(models) == 0 {
		return []ProviderModelConfig{}
	}
	for _, model := range models {
		if model.Enabled {
			return []ProviderModelConfig{model}
		}
	}
	return []ProviderModelConfig{models[0]}
}

func normalizeProviderModel(provider ProviderConfig, source ProviderModelConfig) (ProviderModelConfig, error) {
	model := source
	model.ID = strings.TrimSpace(source.ID)
	model.ModelID = strings.TrimSpace(source.ModelID)
	model.DisplayName = strings.TrimSpace(provider.Name) + "-" + model.ModelID
	model.TooltipData = strings.TrimSpace(source.TooltipData)
	if model.TooltipData == "" {
		model.TooltipData = provider.Name
	}
	model.Enabled = true
	model.ReasoningEffort = normalizeReasoningEffort(source.ReasoningEffort)
	model.OpenAIEndpoint = modelchannel.NormalizeOpenAIEndpoint(provider.Type, source.OpenAIEndpoint)
	model.OpenAIExtraParamsJSON = strings.TrimSpace(source.OpenAIExtraParamsJSON)
	model.AnthropicExtraParamsJSON = strings.TrimSpace(source.AnthropicExtraParamsJSON)
	model.ContextWindowTokens = normalizeMaxCompletionTokens(source.ContextWindowTokens)
	model.MaxCompletionTokens = normalizeMaxCompletionTokens(source.MaxCompletionTokens)
	model.AnthropicMaxTokens = normalizeMaxCompletionTokens(source.AnthropicMaxTokens)
	model.ThinkingBudgetTokens = normalizeMaxCompletionTokens(source.ThinkingBudgetTokens)
	model.LegacyChannelIDs = uniqueTrimmedStrings(source.LegacyChannelIDs)
	if model.ID == "" {
		model.ID = stableConfigID("model", provider.ID+"\n"+strings.ToLower(model.ModelID))
	}
	if model.DisplayName == "" {
		model.DisplayName = model.ModelID
	}
	if model.TooltipData == "" {
		model.TooltipData = provider.Name
	}
	if source.ID == "" && !source.Available {
		model.Available = true
	}
	switch {
	case model.ModelID == "":
		return ProviderModelConfig{}, errors.New("modelID 不能为空")
	case provider.Type == "openai" && model.ReasoningEffort == "":
		return ProviderModelConfig{}, errors.New("reasoningEffort 仅支持 low、medium、high、xhigh、max")
	case provider.Type == "openai" && model.OpenAIEndpoint == "":
		return ProviderModelConfig{}, errors.New("openAIEndpoint 不受支持")
	case provider.Type == "openai" && model.OpenAIExtraParamsEnabled:
		if err := validateJSONMap(model.OpenAIExtraParamsJSON, "openAIExtraParamsJSON"); err != nil {
			return ProviderModelConfig{}, err
		}
	case provider.Type == "anthropic" && model.AnthropicExtraParamsEnabled:
		if err := validateJSONMap(model.AnthropicExtraParamsJSON, "anthropicExtraParamsJSON"); err != nil {
			return ProviderModelConfig{}, err
		}
	}
	if provider.Type == "anthropic" {
		model.AnthropicThinkingEffort = normalizeAnthropicThinkingEffort(source.AnthropicThinkingEffort)
		if model.AnthropicThinkingEffort == "" {
			return ProviderModelConfig{}, errors.New("anthropicThinkingEffort 不受支持")
		}
	} else {
		model.AnthropicThinkingEffort = ""
		model.AnthropicExtraParamsEnabled = false
		model.AnthropicExtraParamsJSON = ""
	}
	return model, nil
}

func ProjectEnabledModelAdapters(providers []ProviderConfig) ([]ModelAdapterConfig, error) {
	adapters := make([]ModelAdapterConfig, 0)
	seenChannels := make(map[string]struct{})
	for _, provider := range providers {
		for _, model := range provider.Models {
			if !model.Enabled {
				continue
			}
			channelID := modelchannel.BuildStableChannelID(provider.ID, model.ID)
			if _, exists := seenChannels[channelID]; exists {
				return nil, fmt.Errorf("启用模型渠道重复: %s", channelID)
			}
			seenChannels[channelID] = struct{}{}
			adapters = append(adapters, ModelAdapterConfig{
				ID:                          channelID,
				ProviderID:                  provider.ID,
				ModelConfigID:               model.ID,
				LegacyChannelIDs:            append([]string(nil), model.LegacyChannelIDs...),
				DisplayName:                 model.DisplayName,
				Type:                        provider.Type,
				BaseURL:                     provider.BaseURL,
				APIKey:                      provider.APIKey,
				TooltipData:                 model.TooltipData,
				ModelID:                     model.ModelID,
				ReasoningEffort:             model.ReasoningEffort,
				OpenAIEndpoint:              model.OpenAIEndpoint,
				OpenAIExtraParamsEnabled:    model.OpenAIExtraParamsEnabled,
				OpenAIExtraParamsJSON:       model.OpenAIExtraParamsJSON,
				CustomHeadersEnabled:        provider.CustomHeadersEnabled,
				CustomHeadersJSON:           provider.CustomHeadersJSON,
				AnthropicExtraParamsEnabled: model.AnthropicExtraParamsEnabled,
				AnthropicExtraParamsJSON:    model.AnthropicExtraParamsJSON,
				ContextWindowTokens:         model.ContextWindowTokens,
				MaxCompletionTokens:         model.MaxCompletionTokens,
				AnthropicMaxTokens:          model.AnthropicMaxTokens,
				AnthropicThinkingEffort:     model.AnthropicThinkingEffort,
				ThinkingBudgetTokens:        model.ThinkingBudgetTokens,
			})
		}
	}
	return adapters, nil
}

func MigrateModelAdaptersToProviders(input []ModelAdapterConfig) ([]ProviderConfig, error) {
	if len(input) == 0 {
		return []ProviderConfig{}, nil
	}
	type providerGroup struct {
		provider ProviderConfig
		models   map[string]int
	}
	groups := make([]providerGroup, 0)
	groupIndex := make(map[string]int)
	for adapterIndex, source := range input {
		baseURL, err := modelchannel.NormalizeBaseURL(source.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("迁移旧模型 %d: %w", adapterIndex+1, err)
		}
		providerType := normalizeModelAdapterType(source.Type)
		groupKey := strings.Join([]string{providerType, baseURL, strings.TrimSpace(source.APIKey), fmt.Sprint(source.CustomHeadersEnabled), strings.TrimSpace(source.CustomHeadersJSON)}, "\n")
		index, exists := groupIndex[groupKey]
		if !exists {
			name := strings.TrimSpace(source.DisplayName)
			if name == "" {
				name = fmt.Sprintf("中转站 %d", len(groups)+1)
			}
			providerID := stableConfigID("provider", groupKey)
			index = len(groups)
			groupIndex[groupKey] = index
			groups = append(groups, providerGroup{
				provider: ProviderConfig{ID: providerID, Name: name, Type: providerType, BaseURL: baseURL, APIKey: strings.TrimSpace(source.APIKey), DiscoveryPath: "/models", CustomHeadersEnabled: source.CustomHeadersEnabled, CustomHeadersJSON: strings.TrimSpace(source.CustomHeadersJSON), Models: []ProviderModelConfig{}},
				models:   make(map[string]int),
			})
		}
		modelIDs := splitLegacyModelIDs(source.ModelID)
		for _, modelID := range modelIDs {
			modelKey := strings.ToLower(modelID)
			legacyIDs := uniqueTrimmedStrings([]string{
				modelchannel.BuildLegacyChannelID(baseURL, modelID, source.APIKey, source.DisplayName),
				modelchannel.BuildChannelID(baseURL, modelID, source.APIKey, source.DisplayName, source.OpenAIEndpoint),
			})
			if existing, duplicate := groups[index].models[modelKey]; duplicate {
				groups[index].provider.Models[existing].LegacyChannelIDs = uniqueTrimmedStrings(append(groups[index].provider.Models[existing].LegacyChannelIDs, legacyIDs...))
				continue
			}
			model := ProviderModelConfig{
				ID: stableConfigID("model", groups[index].provider.ID+"\n"+modelKey), ModelID: modelID,
				DisplayName: buildLegacyModelDisplayName(source.DisplayName, modelID, len(modelIDs) > 1), TooltipData: strings.TrimSpace(source.TooltipData),
				Enabled: true, Available: true, LegacyChannelIDs: legacyIDs, ReasoningEffort: source.ReasoningEffort,
				OpenAIEndpoint: source.OpenAIEndpoint, OpenAIExtraParamsEnabled: source.OpenAIExtraParamsEnabled, OpenAIExtraParamsJSON: source.OpenAIExtraParamsJSON,
				AnthropicExtraParamsEnabled: source.AnthropicExtraParamsEnabled, AnthropicExtraParamsJSON: source.AnthropicExtraParamsJSON,
				ContextWindowTokens: source.ContextWindowTokens, MaxCompletionTokens: source.MaxCompletionTokens, AnthropicMaxTokens: source.AnthropicMaxTokens,
				AnthropicThinkingEffort: source.AnthropicThinkingEffort, ThinkingBudgetTokens: source.ThinkingBudgetTokens,
			}
			groups[index].models[modelKey] = len(groups[index].provider.Models)
			groups[index].provider.Models = append(groups[index].provider.Models, model)
		}
	}
	providers := make([]ProviderConfig, 0, len(groups))
	for _, group := range groups {
		providers = append(providers, group.provider)
	}
	return NormalizeProviderConfigs(providers)
}

func stableConfigID(prefix string, seed string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(seed)))
	return prefix + "_" + hex.EncodeToString(hash[:])[:16]
}

func normalizeDiscoveryPath(value string) string {
	path := strings.TrimSpace(value)
	if path == "" {
		return "/models"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func splitLegacyModelIDs(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == '，' || r == '；'
	})
	return uniqueTrimmedStrings(fields)
}

func buildLegacyModelDisplayName(displayName string, modelID string, expanded bool) string {
	name := strings.TrimSpace(displayName)
	if !expanded {
		return name
	}
	if strings.Contains(name, "{model}") {
		return strings.TrimSpace(strings.ReplaceAll(name, "{model}", modelID))
	}
	return modelID
}

func uniqueTrimmedStrings(input []string) []string {
	output := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, value := range input {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		output = append(output, trimmed)
	}
	sort.SliceStable(output, func(i, j int) bool { return strings.ToLower(output[i]) < strings.ToLower(output[j]) })
	return output
}
