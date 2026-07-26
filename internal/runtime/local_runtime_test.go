package runtime

import (
	"context"
	"testing"

	"cursor/internal/modelchannel"
)

func TestNormalizeModelAdapterConfigsExpandsConfiguredModelIDs(t *testing.T) {
	adapters, err := NormalizeModelAdapterConfigs([]ModelAdapterConfig{{
		DisplayName:     "Broker {model}",
		Type:            "openai",
		BaseURL:         "https://example.com/v1",
		APIKey:          "test-key",
		TooltipData:     "Broker models",
		ModelID:         "model-a\nmodel-b, model-a",
		ReasoningEffort: "medium",
		OpenAIEndpoint:  "/v1/responses",
	}})
	if err != nil {
		t.Fatalf("normalize adapters: %v", err)
	}
	if len(adapters) != 2 {
		t.Fatalf("expected 2 expanded adapters, got %d", len(adapters))
	}
	for index, expected := range []struct {
		name  string
		model string
	}{
		{name: "Broker model-a", model: "model-a"},
		{name: "Broker model-b", model: "model-b"},
	} {
		if adapters[index].DisplayName != expected.name {
			t.Fatalf("adapter %d: expected display name %q, got %q", index, expected.name, adapters[index].DisplayName)
		}
		if adapters[index].ModelID != expected.model {
			t.Fatalf("adapter %d: expected model %q, got %q", index, expected.model, adapters[index].ModelID)
		}
	}
}

func TestConfigurableChannelServiceSelectsExpandedModelID(t *testing.T) {
	service := NewConfigurableChannelService(func(context.Context) (RuntimeConfigSnapshot, error) {
		return RuntimeConfigSnapshot{ModelAdapters: []ModelAdapterConfig{{
			DisplayName:     "Broker {model}",
			Type:            "openai",
			BaseURL:         "https://example.com/v1",
			APIKey:          "test-key",
			TooltipData:     "Broker models",
			ModelID:         "model-a\nmodel-b",
			ReasoningEffort: "high",
			OpenAIEndpoint:  "/v1/responses",
		}}}, nil
	}, "")

	channelID := modelchannel.BuildChannelID("https://example.com/v1", "model-b", "test-key", "Broker model-b", "/v1/responses")
	resolved, err := service.SelectChannelForModel(context.Background(), channelID)
	if err != nil {
		t.Fatalf("select expanded channel: %v", err)
	}
	if resolved.Model != "model-b" {
		t.Fatalf("expected provider model %q, got %q", "model-b", resolved.Model)
	}
	if resolved.Name != "Broker model-b" {
		t.Fatalf("expected channel name %q, got %q", "Broker model-b", resolved.Name)
	}
	if resolved.ReasoningEffort != "high" {
		t.Fatalf("expected reasoning effort %q, got %q", "high", resolved.ReasoningEffort)
	}
}
