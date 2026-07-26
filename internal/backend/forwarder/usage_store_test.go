package forwarder

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUsageStoreUpsertReattributesProviderEventIdempotently(t *testing.T) {
	store := &UsageFileStore{path: filepath.Join(t.TempDir(), usageFileName)}
	at := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	first := usageFileEvent{
		EventID: "request-1", Kind: usageEventKindProvider, ProviderID: "provider-a", ModelConfigID: "model-a", Model: "alpha", At: at,
		InputTokens: 100, OutputTokens: 20, CacheReadTokens: 5, UsagePresent: true,
	}
	if err := store.UpsertEvent(first); err != nil {
		t.Fatalf("insert usage event: %v", err)
	}
	if err := store.UpsertEvent(first); err != nil {
		t.Fatalf("replay usage event: %v", err)
	}

	doc, err := readUsageFileDocument(store.path)
	if err != nil {
		t.Fatalf("read usage document: %v", err)
	}
	assertUsageTotals(t, doc, 1, 125)
	if got := doc.ByProvider["provider-a"].TotalTokens; got != 125 {
		t.Fatalf("expected provider-a total 125, got %d", got)
	}

	updated := first
	updated.ProviderID = "provider-b"
	updated.ModelConfigID = "model-b"
	updated.Model = "beta"
	updated.InputTokens = 200
	updated.OutputTokens = 50
	updated.CacheReadTokens = 0
	if err := store.UpsertEvent(updated); err != nil {
		t.Fatalf("reattribute usage event: %v", err)
	}
	doc, err = readUsageFileDocument(store.path)
	if err != nil {
		t.Fatalf("read updated usage document: %v", err)
	}
	assertUsageTotals(t, doc, 1, 250)
	if got := doc.ByProvider["provider-a"].TotalTokens; got != 0 {
		t.Fatalf("expected old provider total to roll back, got %d", got)
	}
	if got := doc.ByProvider["provider-b"].TotalTokens; got != 250 {
		t.Fatalf("expected provider-b total 250, got %d", got)
	}
	if got := doc.ByProviderModel["provider-b/model-b"].TotalTokens; got != 250 {
		t.Fatalf("expected provider model total 250, got %d", got)
	}
}

func assertUsageTotals(t *testing.T, doc usageFileDocument, calls int64, tokens int64) {
	t.Helper()
	if doc.Totals.ProviderCalls != calls || doc.Totals.TotalTokens != tokens {
		t.Fatalf("unexpected totals: calls=%d tokens=%d", doc.Totals.ProviderCalls, doc.Totals.TotalTokens)
	}
}
