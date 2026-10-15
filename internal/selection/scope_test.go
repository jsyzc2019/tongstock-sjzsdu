package selection

import (
	"testing"

	"github.com/sjzsdu/tongstock/internal/marketsnapshot"
	"github.com/sjzsdu/tongstock/internal/methods"
)

func TestResolveScopeDoesNotRequireUniverseNameEquality(t *testing.T) {
	minCap, maxCap := 30.0, 100.0
	m := &methods.CompiledMethod{Scope: methods.Scope{Universe: "generated_scope", BoardFilter: []string{"main"}, MarketCapMin: &minCap, MarketCapMax: &maxCap}}
	market := &marketsnapshot.MarketSnapshot{Universe: marketsnapshot.UniverseDefinition{Name: "universe_usable"}, UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Board: "main", Status: "normal", Selected: true}}}
	ok, reason, _ := ResolveScope(m, market, "000001", map[string]float64{"market_cap": 50})
	if !ok || reason != "" {
		t.Fatalf("structured scope should match despite universe label: ok=%v reason=%s", ok, reason)
	}
}

func TestResolveScopeFailsClosedWhenRequiredMarketCapMissing(t *testing.T) {
	minCap := 30.0
	m := &methods.CompiledMethod{Scope: methods.Scope{MarketCapMin: &minCap}}
	market := &marketsnapshot.MarketSnapshot{UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Board: "main", Status: "normal", Selected: true}}}
	ok, reason, detail := ResolveScope(m, market, "000001", map[string]float64{})
	if ok || reason != "scope_data_unavailable" || detail == "" {
		t.Fatalf("missing market cap must be excluded: ok=%v reason=%s detail=%s", ok, reason, detail)
	}
}

func TestResolveScopeAvgSharesPerHolderBounds(t *testing.T) {
	min, max := 5000.0, 20000.0
	m := &methods.CompiledMethod{Scope: methods.Scope{AvgSharesPerHolderMin: &min, AvgSharesPerHolderMax: &max}}
	market := &marketsnapshot.MarketSnapshot{UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Board: "main", Status: "normal", Selected: true}}}
	if ok, reason, _ := ResolveScope(m, market, "000001", map[string]float64{"avg_shares_per_holder": 8000}); !ok || reason != "" {
		t.Fatalf("holder value inside bounds should pass: ok=%v reason=%s", ok, reason)
	}
	if ok, _, detail := ResolveScope(m, market, "000001", map[string]float64{"avg_shares_per_holder": 4000}); ok {
		t.Fatalf("holder value below min should be excluded, detail=%s", detail)
	}
	if ok, _, detail := ResolveScope(m, market, "000001", map[string]float64{"avg_shares_per_holder": 30000}); ok {
		t.Fatalf("holder value above max should be excluded, detail=%s", detail)
	}
	ok, reason, detail := ResolveScope(m, market, "000001", map[string]float64{})
	if ok || reason != "scope_data_unavailable" || detail == "" {
		t.Fatalf("missing holder value must fail closed: ok=%v reason=%s detail=%s", ok, reason, detail)
	}
}

func TestCompileCandidateCarriesAvgSharesPerHolder(t *testing.T) {
	min, max := 5000.0, 20000.0
	c := &methods.Candidate{Name: "T", AvgSharesPerHolderMin: &min, AvgSharesPerHolderMax: &max,
		Entry: map[string]any{"type": "compare", "left": map[string]any{"type": "indicator", "indicator": "close"},
			"right": map[string]any{"type": "constant", "value": 1.0}, "op": "gt"}}
	compiled, _, err := methods.Compile(c)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if compiled.Scope.AvgSharesPerHolderMin == nil || *compiled.Scope.AvgSharesPerHolderMin != min ||
		compiled.Scope.AvgSharesPerHolderMax == nil || *compiled.Scope.AvgSharesPerHolderMax != max {
		t.Fatalf("scope bounds lost during compile: %+v", compiled.Scope)
	}
}

func TestResolveScopeAvgTurnoverBounds(t *testing.T) {
	min, max := 3.48, 20.0
	m := &methods.CompiledMethod{Scope: methods.Scope{AvgTurnoverMin: &min, AvgTurnoverMax: &max}}
	market := &marketsnapshot.MarketSnapshot{UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Board: "main", Status: "normal", Selected: true}}}
	if ok, reason, _ := ResolveScope(m, market, "000001", map[string]float64{"avg_turnover": 5.0}); !ok || reason != "" {
		t.Fatalf("avg turnover 5%% inside bounds should pass: ok=%v reason=%s", ok, reason)
	}
	if ok, _, detail := ResolveScope(m, market, "000001", map[string]float64{"avg_turnover": 1.2}); ok {
		t.Fatalf("avg turnover below min should be excluded, detail=%s", detail)
	}
	if ok, _, detail := ResolveScope(m, market, "000001", map[string]float64{"avg_turnover": 25.0}); ok {
		t.Fatalf("avg turnover above max should be excluded, detail=%s", detail)
	}
	ok, reason, detail := ResolveScope(m, market, "000001", map[string]float64{})
	if ok || reason != "scope_data_unavailable" || detail == "" {
		t.Fatalf("missing avg turnover must fail closed: ok=%v reason=%s detail=%s", ok, reason, detail)
	}
}

func TestCompileCandidateCarriesAvgTurnover(t *testing.T) {
	min := 3.4828
	c := &methods.Candidate{Name: "T", AvgTurnoverMin: &min,
		Entry: map[string]any{"type": "compare", "left": map[string]any{"type": "indicator", "indicator": "close"},
			"right": map[string]any{"type": "constant", "value": 1.0}, "op": "gt"}}
	compiled, _, err := methods.Compile(c)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if compiled.Scope.AvgTurnoverMin == nil || *compiled.Scope.AvgTurnoverMin != min {
		t.Fatalf("avg turnover bound lost during compile: %+v", compiled.Scope)
	}
}

// TestResolveScopeLegacyUniverseLabelIsInformational guards the behavior the
// mined-method blocker fix depends on: a legacy method carrying only a
// Universe label (no structured scope) must NOT be rejected for the label
// differing from the snapshot universe name — it intentionally expands to the
// full snapshot membership instead of staying confined to its research pool.
func TestResolveScopeLegacyUniverseLabelIsInformational(t *testing.T) {
	m := &methods.CompiledMethod{Scope: methods.Scope{Universe: "researched_stocks"}}
	market := &marketsnapshot.MarketSnapshot{
		Universe:        marketsnapshot.UniverseDefinition{Name: "universe_usable"},
		UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Board: "main", Status: "normal", Selected: true}},
	}
	ok, reason, detail := ResolveScope(m, market, "000001", map[string]float64{})
	if !ok || reason != "" {
		t.Fatalf("legacy universe label must not gate screening: ok=%v reason=%s detail=%s", ok, reason, detail)
	}
}
