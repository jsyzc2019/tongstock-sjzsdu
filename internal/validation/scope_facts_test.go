package validation

import (
	"testing"

	"github.com/sjzsdu/tongstock/internal/methods"
)

func f32(v float64) *float64 { return &v }

func scopeBars(codes ...string) []BacktestBar {
	bars := make([]BacktestBar, 0, len(codes))
	for _, c := range codes {
		bars = append(bars, BacktestBar{Code: c, Date: "2024-01-02", Close: 10, TurnoverRate: 5.0})
	}
	return bars
}

func TestScopeSkipReasonTurnover(t *testing.T) {
	scope := methods.Scope{AvgTurnoverMin: f32(3.4828)}
	facts := scopeFacts{avgTurnover: 5.0}
	if reason := scopeSkipReason(scope, facts); reason != "" {
		t.Fatalf("turnover 5.0 should pass min 3.4828: %s", reason)
	}
	low := scopeFacts{avgTurnover: 1.2}
	if reason := scopeSkipReason(scope, low); reason == "" {
		t.Fatal("turnover 1.2 below min must be excluded")
	}
	unknown := scopeFacts{avgTurnover: 0}
	if reason := scopeSkipReason(scope, unknown); reason == "" {
		t.Fatal("unknown turnover must fail closed")
	}
}

func TestScopeSkipReasonCapAndHolder(t *testing.T) {
	scope := methods.Scope{MarketCapMin: f32(6.08), MarketCapMax: f32(39.5)}
	ok := scopeFacts{marketCapYi: 20}
	if reason := scopeSkipReason(scope, ok); reason != "" {
		t.Fatalf("cap 20亿 in [6.08,39.5] should pass: %s", reason)
	}
	if reason := scopeSkipReason(scope, scopeFacts{marketCapYi: 100}); reason == "" {
		t.Fatal("cap 100亿 above max must be excluded")
	}
	if reason := scopeSkipReason(scope, scopeFacts{}); reason == "" {
		t.Fatal("unknown cap must fail closed")
	}

	holder := methods.Scope{AvgSharesPerHolderMin: f32(601), AvgSharesPerHolderMax: f32(10232)}
	if reason := scopeSkipReason(holder, scopeFacts{perHolder: 5000}); reason != "" {
		t.Fatalf("perHolder 5000 in range should pass: %s", reason)
	}
	if reason := scopeSkipReason(holder, scopeFacts{perHolder: 20000}); reason == "" {
		t.Fatal("perHolder 20000 above max must be excluded")
	}
}

func TestComputeScopeFactsNeedsProviderForCap(t *testing.T) {
	// scope 要求市值但未装配 facts provider → 必须报错，不静默放行。
	scope := methods.Scope{MarketCapMin: f32(6)}
	if _, err := computeScopeFacts(t.Context(), scope, scopeBars("000001"), nil); err == nil {
		t.Fatal("cap scope without facts provider must fail closed")
	}
	// 只要求换手率（bars 可算）时不需要 provider。
	turnoverOnly := methods.Scope{AvgTurnoverMin: f32(1)}
	facts, err := computeScopeFacts(t.Context(), turnoverOnly, scopeBars("000001"), nil)
	if err != nil {
		t.Fatalf("turnover scope should not need provider: %v", err)
	}
	if facts.avgTurnover != 5.0 {
		t.Fatalf("avgTurnover = %v, want 5.0", facts.avgTurnover)
	}
}
