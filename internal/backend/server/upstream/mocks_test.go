package upstream

import (
	"encoding/json"
	"testing"

	legacyruntime "cursor/internal/runtime"
)

func TestBuildAvailableModelEntriesUsesConfiguredModelsOnly(t *testing.T) {
	entries := buildAvailableModelEntries([]legacyruntime.ModelAdapterConfig{
		{ID: "first", DisplayName: "First", ModelID: "model-first", Type: "openai", ReasoningEffort: "medium", TooltipData: "First model"},
		{ID: "second", DisplayName: "Second", ModelID: "model-second", Type: "anthropic", AnthropicThinkingEffort: "xhigh", TooltipData: "Second model"},
		{ID: "third", DisplayName: "Third", ModelID: "model-third", Type: "openai", ReasoningEffort: "high", TooltipData: "Third model"},
	})

	if availableModelsDisableUnusedHours != 0 {
		t.Fatalf("unused models must expire immediately, got %d hours", availableModelsDisableUnusedHours)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 configured entries, got %d", len(entries))
	}
	for index, expected := range []struct {
		id      string
		tagline string
	}{
		{id: "first", tagline: "Medium"},
		{id: "second", tagline: "XHigh"},
		{id: "third", tagline: "High"},
	} {
		if actualID, _ := entries[index]["name"].(string); actualID != expected.id {
			t.Fatalf("entry %d: expected model %q, got %q", index, expected.id, actualID)
		}
		if actualTagline, _ := entries[index]["tagline"].(string); actualTagline != expected.tagline {
			t.Fatalf("entry %d: expected tagline %q, got %q", index, expected.tagline, actualTagline)
		}
		variants, _ := entries[index]["variants"].([]map[string]any)
		if len(variants) != 1 {
			t.Fatalf("entry %d: expected exactly one runtime variant, got %d", index, len(variants))
		}
	}
}

func TestBuildBootstrapStatsigConfigDisablesDecomposeAlwaysLocalExtHost(t *testing.T) {
	payload, err := buildBootstrapStatsigConfigJSON(12345, "test-auth-id")
	if err != nil {
		t.Fatalf("build bootstrap statsig config: %v", err)
	}

	var decoded statsigBootstrapTemplate
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode bootstrap statsig config: %v", err)
	}

	gate, ok := decoded.FeatureGates[bootstrapStatsigDecomposeAlwaysLocalExtHostGate]
	if !ok {
		t.Fatalf("missing feature gate %q", bootstrapStatsigDecomposeAlwaysLocalExtHostGate)
	}
	if value, _ := gate["value"].(bool); value {
		t.Fatalf("expected %q to be disabled", bootstrapStatsigDecomposeAlwaysLocalExtHostGate)
	}
	if ruleID, _ := gate["rule_id"].(string); ruleID != "local_disabled" {
		t.Fatalf("unexpected rule_id: %q", ruleID)
	}
}
