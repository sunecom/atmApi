package model

import (
	"errors"
	"strings"
)

const (
	CostSourceUpstreamReported   = "upstream_reported"
	CostSourcePricingSnapshot    = "pricing_snapshot"
	CostSourceLocalResponseCache = "local_response_cache"
	CostSourceSingleflightShared = "singleflight_shared"
	GLM52UsagePolicyVersion      = "glm52-v0.2.1"
)

// ProviderPriceSnapshot is an immutable, auditable view of one provider's
// prices. Prices use the snapshot currency and are per million tokens.
type ProviderPriceSnapshot struct {
	ID                        string
	Provider                  string
	Model                     string
	Currency                  string
	InputPricePerMillion      float64
	CachedPricePerMillion     float64
	CacheWritePricePerMillion float64
	OutputPricePerMillion     float64
	ReasoningPricePerMillion  float64
}

type ProviderUsage struct {
	PromptTokens         int64
	CachedTokens         int64
	CacheWriteTokens     int64
	CompletionTokens     int64
	ReasoningTokens      int64
	UpstreamReportedCost *float64
	UpstreamCurrency     string
}

type ProviderCost struct {
	Amount              float64
	Currency            string
	Source              string
	Estimated           bool
	PricingSnapshotID   string
	UncachedInputTokens int64
	CachedTokens        int64
	CacheWriteTokens    int64
	VisibleOutputTokens int64
	ReasoningTokens     int64
}

func CalculateProviderCost(usage ProviderUsage, snapshot ProviderPriceSnapshot) (ProviderCost, error) {
	if usage.UpstreamReportedCost != nil {
		if *usage.UpstreamReportedCost < 0 {
			return ProviderCost{}, errors.New("upstream reported cost cannot be negative")
		}
		currency := strings.ToUpper(strings.TrimSpace(usage.UpstreamCurrency))
		if currency == "" {
			return ProviderCost{}, errors.New("upstream reported cost requires a currency")
		}
		prompt := nonNegative(usage.PromptTokens)
		cached := min64(nonNegative(usage.CachedTokens), prompt)
		cacheWrite := min64(nonNegative(usage.CacheWriteTokens), prompt-cached)
		completion := nonNegative(usage.CompletionTokens)
		reasoning := min64(nonNegative(usage.ReasoningTokens), completion)
		return ProviderCost{Amount: *usage.UpstreamReportedCost, Currency: currency,
			Source: CostSourceUpstreamReported, PricingSnapshotID: snapshot.ID,
			UncachedInputTokens: prompt - cached - cacheWrite, CachedTokens: cached,
			CacheWriteTokens: cacheWrite, VisibleOutputTokens: completion - reasoning,
			ReasoningTokens: reasoning}, nil
	}
	if strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(snapshot.Currency) == "" {
		return ProviderCost{}, errors.New("pricing snapshot id and currency are required")
	}
	if hasNegativePrice(snapshot) {
		return ProviderCost{}, errors.New("pricing snapshot prices cannot be negative")
	}

	prompt := nonNegative(usage.PromptTokens)
	cached := min64(nonNegative(usage.CachedTokens), prompt)
	cacheWrite := min64(nonNegative(usage.CacheWriteTokens), prompt-cached)
	uncached := prompt - cached - cacheWrite
	completion := nonNegative(usage.CompletionTokens)
	reasoning := min64(nonNegative(usage.ReasoningTokens), completion)
	visible := completion - reasoning
	cacheWritePrice := snapshot.CacheWritePricePerMillion
	if cacheWritePrice == 0 {
		cacheWritePrice = snapshot.InputPricePerMillion
	}
	reasoningPrice := snapshot.ReasoningPricePerMillion
	if reasoningPrice == 0 {
		reasoningPrice = snapshot.OutputPricePerMillion
	}
	amount := perMillion(uncached, snapshot.InputPricePerMillion) +
		perMillion(cached, snapshot.CachedPricePerMillion) +
		perMillion(cacheWrite, cacheWritePrice) +
		perMillion(visible, snapshot.OutputPricePerMillion) +
		perMillion(reasoning, reasoningPrice)

	return ProviderCost{Amount: amount, Currency: strings.ToUpper(snapshot.Currency),
		Source: CostSourcePricingSnapshot, Estimated: true, PricingSnapshotID: snapshot.ID,
		UncachedInputTokens: uncached, CachedTokens: cached, CacheWriteTokens: cacheWrite,
		VisibleOutputTokens: visible, ReasoningTokens: reasoning}, nil
}

// GLM52FallbackPriceSnapshot is conservative: until an audited provider cache
// price exists, cache reads use the full input price rather than a guessed 10%.
func GLM52FallbackPriceSnapshot(provider string) ProviderPriceSnapshot {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "unknown"
	}
	return ProviderPriceSnapshot{ID: "glm52-cny-baseline-v021", Provider: provider,
		Model: "z-ai/glm-5.2", Currency: "CNY", InputPricePerMillion: 8,
		CachedPricePerMillion: 8, CacheWritePricePerMillion: 8,
		OutputPricePerMillion: 28, ReasoningPricePerMillion: 28}
}

func hasNegativePrice(s ProviderPriceSnapshot) bool {
	return s.InputPricePerMillion < 0 || s.CachedPricePerMillion < 0 ||
		s.CacheWritePricePerMillion < 0 || s.OutputPricePerMillion < 0 || s.ReasoningPricePerMillion < 0
}
func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func perMillion(tokens int64, price float64) float64 { return float64(tokens) / 1_000_000 * price }
