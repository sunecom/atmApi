package model

import (
	"math"
	"testing"
)

func TestCalculateProviderCostUsesEveryTokenClassWithoutDoubleCountingReasoning(t *testing.T) {
	snapshot := ProviderPriceSnapshot{
		ID:                        "provider-a-v1",
		Provider:                  "provider-a",
		Model:                     "z-ai/glm-5.2",
		Currency:                  "CNY",
		InputPricePerMillion:      8,
		CachedPricePerMillion:     2,
		CacheWritePricePerMillion: 10,
		OutputPricePerMillion:     28,
		ReasoningPricePerMillion:  35,
	}
	usage := ProviderUsage{
		PromptTokens:     1_000_000,
		CachedTokens:     200_000,
		CacheWriteTokens: 100_000,
		CompletionTokens: 300_000,
		ReasoningTokens:  100_000,
	}

	cost, err := CalculateProviderCost(usage, snapshot)
	if err != nil {
		t.Fatalf("CalculateProviderCost() error = %v", err)
	}
	// 700k normal input + 200k cache read + 100k cache write +
	// 200k visible output + 100k reasoning.
	want := 0.7*8 + 0.2*2 + 0.1*10 + 0.2*28 + 0.1*35
	if math.Abs(cost.Amount-want) > 1e-9 {
		t.Fatalf("amount = %.9f, want %.9f; breakdown=%+v", cost.Amount, want, cost)
	}
	if cost.VisibleOutputTokens != 200_000 || cost.UncachedInputTokens != 700_000 {
		t.Fatalf("normalized token classes = %+v", cost)
	}
	if cost.Source != CostSourcePricingSnapshot || cost.Currency != "CNY" || cost.PricingSnapshotID != snapshot.ID {
		t.Fatalf("audit metadata = %+v", cost)
	}
}

func TestCalculateProviderCostPrioritizesUpstreamReportedCost(t *testing.T) {
	reported := 0.012345
	cost, err := CalculateProviderCost(ProviderUsage{
		PromptTokens:         999_999,
		CompletionTokens:     999_999,
		UpstreamReportedCost: &reported,
		UpstreamCurrency:     "USD",
	}, ProviderPriceSnapshot{
		ID: "fallback", Currency: "CNY",
		InputPricePerMillion: 8, OutputPricePerMillion: 28,
	})
	if err != nil {
		t.Fatalf("CalculateProviderCost() error = %v", err)
	}
	if cost.Amount != reported || cost.Currency != "USD" || cost.Source != CostSourceUpstreamReported {
		t.Fatalf("reported cost was not authoritative: %+v", cost)
	}
	if cost.Estimated {
		t.Fatalf("reported cost must not be marked estimated: %+v", cost)
	}
}

func TestCalculateProviderCostDiffersByProviderSnapshot(t *testing.T) {
	usage := ProviderUsage{PromptTokens: 1_000_000, CachedTokens: 500_000, CompletionTokens: 100_000}
	providerA := ProviderPriceSnapshot{ID: "a", Provider: "a", Currency: "USD", InputPricePerMillion: 2, CachedPricePerMillion: 1, OutputPricePerMillion: 8}
	providerB := ProviderPriceSnapshot{ID: "b", Provider: "b", Currency: "USD", InputPricePerMillion: 4, CachedPricePerMillion: 0.5, OutputPricePerMillion: 12}

	a, err := CalculateProviderCost(usage, providerA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CalculateProviderCost(usage, providerB)
	if err != nil {
		t.Fatal(err)
	}
	if a.Amount == b.Amount {
		t.Fatalf("provider snapshots collapsed to one price: a=%+v b=%+v", a, b)
	}
}

func TestGLM52FallbackSnapshotDoesNotInventTenPercentCacheDiscount(t *testing.T) {
	snapshot := GLM52FallbackPriceSnapshot("OpenRouter")
	if snapshot.CachedPricePerMillion != snapshot.InputPricePerMillion {
		t.Fatalf("unknown cache price must be conservative, got cached=%v input=%v", snapshot.CachedPricePerMillion, snapshot.InputPricePerMillion)
	}
}

func TestLegacyCalculateCostDoesNotApplyTenPercentCacheRateToGLM52(t *testing.T) {
	got := CalculateCost(1_000, 0, 500, "z-ai/glm-5.2")
	if math.Abs(got-0.008) > 1e-12 {
		t.Fatalf("CalculateCost(GLM-5.2) = %.9f, want conservative full input cost", got)
	}
}
