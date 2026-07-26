package config

import (
	"testing"

	"cursor/internal/modelchannel"
)

func TestProjectEnabledModelAdaptersKeepsProvidersDistinct(t *testing.T) {
	providers, err := NormalizeProviderConfigs([]ProviderConfig{
		{
			ID: "provider-a", Name: "A", Type: "openai", BaseURL: "https://a.example/v1", APIKey: "key-a",
			Models: []ProviderModelConfig{{ID: "model-a", ModelID: "shared-model", Enabled: true, Available: true}},
		},
		{
			ID: "provider-b", Name: "B", Type: "openai", BaseURL: "https://b.example/v1", APIKey: "key-b",
			Models: []ProviderModelConfig{{ID: "model-b", ModelID: "shared-model", Enabled: true, Available: true}},
		},
	})
	if err != nil {
		t.Fatalf("normalize providers: %v", err)
	}

	adapters, err := ProjectEnabledModelAdapters(providers)
	if err != nil {
		t.Fatalf("project adapters: %v", err)
	}
	if len(adapters) != 2 {
		t.Fatalf("expected 2 enabled adapters, got %d", len(adapters))
	}
	if adapters[0].ID == adapters[1].ID {
		t.Fatalf("same model on different providers must have distinct channel IDs")
	}
	if _, ok := resolveProjectedAdapterIndex(adapters, "shared-model"); ok {
		t.Fatalf("ambiguous upstream model ID must not select an arbitrary provider")
	}
	for index, adapter := range adapters {
		resolved, err := resolveModelAdapterChannel(adapters, adapter.ID)
		if err != nil {
			t.Fatalf("resolve adapter %d by stable ID: %v", index, err)
		}
		if resolved.ProviderID != adapter.ProviderID || resolved.ModelConfigID != adapter.ModelConfigID {
			t.Fatalf("adapter %d attribution mismatch: %#v", index, resolved)
		}
	}
}

func TestMigrateModelAdaptersPreservesEndpointChannelID(t *testing.T) {
	legacy := ModelAdapterConfig{
		DisplayName: "Legacy", Type: "openai", BaseURL: "https://example.com/v1", APIKey: "key",
		TooltipData: "legacy", ModelID: "model-a", ReasoningEffort: "medium", OpenAIEndpoint: "/v1/responses",
	}
	providers, err := MigrateModelAdaptersToProviders([]ModelAdapterConfig{legacy})
	if err != nil {
		t.Fatalf("migrate adapters: %v", err)
	}
	if len(providers) != 1 || len(providers[0].Models) != 1 {
		t.Fatalf("unexpected migrated shape: %#v", providers)
	}

	expected := modelchannel.BuildChannelID(legacy.BaseURL, legacy.ModelID, legacy.APIKey, legacy.DisplayName, legacy.OpenAIEndpoint)
	model := providers[0].Models[0]
	if !containsString(model.LegacyChannelIDs, expected) {
		t.Fatalf("expected endpoint-aware legacy channel %q in %#v", expected, model.LegacyChannelIDs)
	}
	adapters, err := ProjectEnabledModelAdapters(providers)
	if err != nil {
		t.Fatalf("project migrated adapters: %v", err)
	}
	resolved, err := resolveModelAdapterChannel(adapters, expected)
	if err != nil {
		t.Fatalf("resolve migrated endpoint-aware legacy channel: %v", err)
	}
	if resolved.Model != legacy.ModelID {
		t.Fatalf("expected model %q, got %q", legacy.ModelID, resolved.Model)
	}
}

func TestNormalizeProviderConfigsKeepsStableIDsAfterMutableFieldsChange(t *testing.T) {
	initial, err := NormalizeProviderConfigs([]ProviderConfig{{
		Name: "Original", Type: "openai", BaseURL: "https://example.com/v1", APIKey: "old-key",
		Models: []ProviderModelConfig{{ModelID: "model-a", Enabled: true, Available: true}},
	}})
	if err != nil {
		t.Fatalf("normalize initial provider: %v", err)
	}
	changed := initial[0]
	changed.Name = "Renamed"
	changed.BaseURL = "https://other.example/v1"
	changed.APIKey = "new-key"

	normalized, err := NormalizeProviderConfigs([]ProviderConfig{changed})
	if err != nil {
		t.Fatalf("normalize changed provider: %v", err)
	}
	if normalized[0].ID != initial[0].ID || normalized[0].Models[0].ID != initial[0].Models[0].ID {
		t.Fatalf("persisted stable IDs changed after mutable fields update")
	}
}

func containsString(items []string, expected string) bool {
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}
