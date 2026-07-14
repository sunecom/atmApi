package service

import (
	"math"
	"testing"
)

func TestEstimateSavingsWithPricesUsesExplicitCachePrice(t *testing.T) {
	got := EstimateSavingsWithPrices(1_000, 600, 0.008, 0.006)
	want := float64(600) / 1000 * (0.008 - 0.006)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("EstimateSavingsWithPrices() = %.12f, want %.12f", got, want)
	}
}

func TestEstimateSavingsWithPricesDoesNotReportNegativeSavings(t *testing.T) {
	if got := EstimateSavingsWithPrices(1_000, 600, 0.008, 0.010); got != 0 {
		t.Fatalf("more expensive cache must not be reported as savings, got %v", got)
	}
}
