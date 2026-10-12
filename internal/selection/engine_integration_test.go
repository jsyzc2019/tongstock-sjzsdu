package selection_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sjzsdu/tongstock/internal/adapter/marketsnapshotrepo"
	"github.com/sjzsdu/tongstock/internal/adapter/methodregistryrepo"
	"github.com/sjzsdu/tongstock/internal/adapter/selectionrepo"
	"github.com/sjzsdu/tongstock/internal/marketsnapshot"
	"github.com/sjzsdu/tongstock/internal/methodregistry"
	"github.com/sjzsdu/tongstock/internal/methods"
	"github.com/sjzsdu/tongstock/internal/selection"
	"github.com/sjzsdu/tongstock/pkg/storage"
)

func TestSelectionPersistsDeterministicTraceableBuy(t *testing.T) {
	ctx := context.Background()
	store, err := storage.New(storage.Config{Driver: "sqlite3", DSN: filepath.Join(t.TempDir(), "selection.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	snapshots, _ := marketsnapshotrepo.New(store)
	methodsRepo, _ := methodregistryrepo.New(store)
	runs, _ := selectionrepo.New(store)

	market := &marketsnapshot.MarketSnapshot{ID: "real-snapshot", SnapshotDate: "2024-01-02", Universe: marketsnapshot.UniverseDefinition{Name: "universe_usable"}, Market: "CN-A", PriceAdjustment: "forward", ExpectedKlineCodes: 1, ReadyKlineCodes: 1, CoveragePct: 1, Status: marketsnapshot.StatusReady, Frozen: false, UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Selected: true, Status: "normal"}}, Codes: []marketsnapshot.CodeStatus{{Code: "000001", UniverseMember: true, KlineLastDate: "2024-01-02", KlineRowCount: 260}}}
	market.UniverseHash = marketsnapshot.ComputeUniverseHash(market.UniverseMembers)
	market.ContentHash, _ = marketsnapshot.ComputeContentHash(market)
	if err := snapshots.SaveMarketSnapshot(market); err != nil {
		t.Fatal(err)
	}
	if err := snapshots.FreezeMarketSnapshot(market.ID); err != nil {
		t.Fatal(err)
	}
	feature := &marketsnapshot.FeatureSnapshot{ID: "real-features", MarketSnapshotID: market.ID, SnapshotDate: market.SnapshotDate, FeatureIDs: []string{"close", "ma20", "amount"}, FeatureTotal: 3, RowsWritten: 3, LeakChecked: true, PriceAdjustment: "forward", Status: marketsnapshot.StatusReady, AsOfNs: time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local).UnixNano(), Values: map[string]map[string]float64{"000001": {"close": 10.5, "ma20": 10, "amount": 100000000}}}
	feature.ContentHash, _ = marketsnapshot.ComputeFeatureContentHash(feature)
	if err := snapshots.SaveFeatureSnapshot(feature); err != nil {
		t.Fatal(err)
	}
	pct, stop, take := .08, -.05, .1
	compiled, _, err := methods.Compile(&methods.Candidate{Name: "收盘站上20日均线", Universe: "universe_usable", Entry: map[string]any{"type": "compare", "left": map[string]any{"type": "indicator", "indicator": "close"}, "right": map[string]any{"type": "indicator", "indicator": "ma20"}, "op": "gt"}, HoldingMaxDays: 10, StopLossPct: &stop, TakeProfitPct: &take, PositionMode: "pct_equity", PositionPct: &pct})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	method := &methodregistry.Method{ID: "method-real", FamilyID: "family-trend", VariantID: "v1", Name: compiled.Name, Status: methodregistry.StatusVerified, Market: "A", Universe: "universe_usable", HoldingMaxDays: 10, CurrentVersion: 1, Versions: []methodregistry.MethodVersion{{ID: "method-real-v1", Version: 1, MethodHash: compiled.ContentHash, Method: compiled, Evidence: &methodregistry.EvidenceSummary{ResultHash: "evidence-real", SnapshotID: "validation-real", Confidence: "strong", Passable: true, OOSTrades: 120, OOSMaxDrawdown: -.1}, CreatedAt: now}}, CreatedAt: now, UpdatedAt: now}
	if err := methodsRepo.Save(ctx, method, methodregistry.AuditEvent{ID: "audit-real", MethodID: method.ID, From: methodregistry.StatusCandidate, To: methodregistry.StatusVerified, Action: "policy", Automatic: true, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	engine, err := selection.NewEngine(snapshots, methodsRepo, runs)
	if err != nil {
		t.Fatal(err)
	}
	first, err := engine.Run(ctx, selection.Request{MarketSnapshotID: market.ID, FeatureSnapshotID: feature.ID})
	if err != nil {
		t.Fatal(err)
	}
	if first.BuyCount != 1 || len(first.Candidates) != 1 || first.Candidates[0].Action != selection.ActionBuy {
		t.Fatalf("unexpected run: %+v", first)
	}
	if first.Candidates[0].SnapshotID != market.ID || first.Candidates[0].Triggers[0].MethodVersionID != "method-real-v1" {
		t.Fatal("candidate lost snapshot/method traceability")
	}
	second, err := engine.Run(ctx, selection.Request{MarketSnapshotID: market.ID, FeatureSnapshotID: feature.ID})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("same immutable inputs did not replay idempotently")
	}
}

// TestSelectionGapPctRuleIsNeverSilentlyEmpty guards the round-2 blocker fix.
// A gap_pct entry rule needs the previous close, but the daily engine executes
// against a frozen single-day feature snapshot ([]methods.Bar{bar}). That means:
//   - when gap_pct is NOT materialized in the snapshot, the stock must be
//     reported as insufficient_data — never zero candidates with zero
//     exclusions (the silent all-miss this test is named after);
//   - when gap_pct IS materialized (DefaultDslFeatures now includes it), the
//     rule is actually evaluated and can produce a candidate.
func TestSelectionGapPctRuleIsNeverSilentlyEmpty(t *testing.T) {
	ctx := context.Background()
	positionPct := 0.1

	setup := func(t *testing.T, values map[string]map[string]float64, featureIDs []string) (*selection.Run, *methodregistry.Method) {
		t.Helper()
		store, err := storage.New(storage.Config{Driver: "sqlite3", DSN: filepath.Join(t.TempDir(), "selection-gappct.db")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Migrate(); err != nil {
			t.Fatal(err)
		}
		snapshots, _ := marketsnapshotrepo.New(store)
		methodsRepo, _ := methodregistryrepo.New(store)
		runs, _ := selectionrepo.New(store)

		market := &marketsnapshot.MarketSnapshot{ID: "real-snapshot", SnapshotDate: "2024-01-02", Universe: marketsnapshot.UniverseDefinition{Name: "universe_usable"}, Market: "CN-A", PriceAdjustment: "forward", ExpectedKlineCodes: 1, ReadyKlineCodes: 1, CoveragePct: 1, Status: marketsnapshot.StatusReady, Frozen: false, UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Selected: true, Status: "normal"}}, Codes: []marketsnapshot.CodeStatus{{Code: "000001", UniverseMember: true, KlineLastDate: "2024-01-02", KlineRowCount: 260}}}
		market.UniverseHash = marketsnapshot.ComputeUniverseHash(market.UniverseMembers)
		market.ContentHash, _ = marketsnapshot.ComputeContentHash(market)
		if err := snapshots.SaveMarketSnapshot(market); err != nil {
			t.Fatal(err)
		}
		if err := snapshots.FreezeMarketSnapshot(market.ID); err != nil {
			t.Fatal(err)
		}
		feature := &marketsnapshot.FeatureSnapshot{ID: "real-features", MarketSnapshotID: market.ID, SnapshotDate: market.SnapshotDate, FeatureIDs: featureIDs, FeatureTotal: len(featureIDs), RowsWritten: len(featureIDs), LeakChecked: true, PriceAdjustment: "forward", Status: marketsnapshot.StatusReady, AsOfNs: time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local).UnixNano(), Values: values}
		feature.ContentHash, _ = marketsnapshot.ComputeFeatureContentHash(feature)
		if err := snapshots.SaveFeatureSnapshot(feature); err != nil {
			t.Fatal(err)
		}
		compiled, _, err := methods.Compile(&methods.Candidate{
			Name: "跳空超过2%", Universe: "universe_usable",
			Entry:          map[string]any{"type": "compare", "left": map[string]any{"type": "indicator", "indicator": "gap_pct"}, "right": map[string]any{"type": "constant", "value": 0.02}, "op": "gt"},
			HoldingMaxDays: 10, PositionMode: "pct_equity", PositionPct: &positionPct,
		})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		method := &methodregistry.Method{ID: "method-gap", FamilyID: "family-gap", VariantID: "v1", Name: compiled.Name, Status: methodregistry.StatusVerified, Market: "A", Universe: "universe_usable", HoldingMaxDays: 10, CurrentVersion: 1, Versions: []methodregistry.MethodVersion{{ID: "method-gap-v1", Version: 1, MethodHash: compiled.ContentHash, Method: compiled, Evidence: &methodregistry.EvidenceSummary{ResultHash: "evidence-gap", SnapshotID: "validation-real", Confidence: "strong", Passable: true, OOSTrades: 120, OOSMaxDrawdown: -.1}, CreatedAt: now}}, CreatedAt: now, UpdatedAt: now}
		if err := methodsRepo.Save(ctx, method, methodregistry.AuditEvent{ID: "audit-method-gap", MethodID: method.ID, From: methodregistry.StatusCandidate, To: methodregistry.StatusVerified, Action: "policy", Automatic: true, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		engine, err := selection.NewEngine(snapshots, methodsRepo, runs)
		if err != nil {
			t.Fatal(err)
		}
		run, err := engine.Run(ctx, selection.Request{MarketSnapshotID: market.ID, FeatureSnapshotID: feature.ID})
		if err != nil {
			t.Fatal(err)
		}
		return run, method
	}

	t.Run("missing gap_pct feature reports insufficient_data", func(t *testing.T) {
		// 模拟旧特征快照：没有物化 gap_pct。
		run, method := setup(t, map[string]map[string]float64{"000001": {"close": 10.5, "open": 10.6, "ma20": 10, "amount": 100000000}}, []string{"close", "open", "ma20", "amount"})
		if len(run.Candidates) != 0 {
			t.Fatalf("expected no candidates, got %d", len(run.Candidates))
		}
		found := false
		for _, x := range run.Exclusions {
			if x.ReasonCode == "insufficient_data" && x.MethodID == method.ID && strings.Contains(x.Detail, "gap_pct") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected insufficient_data exclusion mentioning gap_pct, got %+v", run.Exclusions)
		}
	})

	t.Run("materialized gap_pct feature evaluates the rule", func(t *testing.T) {
		// gap_pct 物化后（DefaultDslFeatures 现已包含），规则真正参与判定。
		run, _ := setup(t, map[string]map[string]float64{"000001": {"close": 10.5, "open": 10.6, "ma20": 10, "amount": 100000000, "gap_pct": 0.05}}, []string{"close", "open", "ma20", "amount", "gap_pct"})
		if len(run.Candidates) != 1 {
			t.Fatalf("expected the gap_pct rule to match and produce a candidate, got %d candidates, exclusions: %+v", len(run.Candidates), run.Exclusions)
		}
		// 单方法触发默认进入观察档；关键是规则被真正判定并产出候选。
		action := run.Candidates[0].Action
		if action != selection.ActionBuy && action != selection.ActionWatch {
			t.Fatalf("expected a real buy/watch candidate, got %s", action)
		}
		for _, x := range run.Exclusions {
			if x.ReasonCode == "insufficient_data" {
				t.Fatalf("materialized feature must not be reported missing: %+v", x)
			}
		}
	})
}

// stubGapFeatureEngine 模拟物化引擎：补齐 gap 类方法所需的当日特征。
type stubGapFeatureEngine struct{}

func (stubGapFeatureEngine) Compute(date string, codes []string, features []marketsnapshot.FeatureSpec) (map[string]map[string]float64, error) {
	out := map[string]map[string]float64{}
	for _, c := range codes {
		out[c] = map[string]float64{"close": 10.5, "open": 10.6, "ma20": 10, "amount": 100000000, "gap_pct": 0.05}
	}
	return out, nil
}

// featureNames 返回特征规格名集合，便于断言快照覆盖关系。
func featureNames(specs []marketsnapshot.FeatureSpec) []string {
	return marketsnapshot.FeatureNames(specs)
}

// TestSelectionPrefersFeatureSnapshotCoveringDefaultDsl reproduces the round-3
// blocker at the snapshot-pick layer: a stale pre-gap_pct feature snapshot can
// be NEWER than the rebuilt one, so newest-first picking selects the snapshot
// that cannot evaluate gap rules and every stock falls into insufficient_data.
// The engine must prefer a snapshot covering the full default DSL feature set.
func TestSelectionPrefersFeatureSnapshotCoveringDefaultDsl(t *testing.T) {
	ctx := context.Background()
	store, err := storage.New(storage.Config{Driver: "sqlite3", DSN: filepath.Join(t.TempDir(), "selection-pref.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	snapshots, _ := marketsnapshotrepo.New(store)
	methodsRepo, _ := methodregistryrepo.New(store)
	runs, _ := selectionrepo.New(store)

	market := &marketsnapshot.MarketSnapshot{ID: "pref-snapshot", SnapshotDate: "2024-01-02", Universe: marketsnapshot.UniverseDefinition{Name: "universe_usable"}, Market: "CN-A", PriceAdjustment: "forward", ExpectedKlineCodes: 1, ReadyKlineCodes: 1, CoveragePct: 1, Status: marketsnapshot.StatusReady, Frozen: false, UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Selected: true, Status: "normal"}}, Codes: []marketsnapshot.CodeStatus{{Code: "000001", UniverseMember: true, KlineLastDate: "2024-01-02", KlineRowCount: 260}}}
	market.UniverseHash = marketsnapshot.ComputeUniverseHash(market.UniverseMembers)
	market.ContentHash, _ = marketsnapshot.ComputeContentHash(market)
	if err := snapshots.SaveMarketSnapshot(market); err != nil {
		t.Fatal(err)
	}
	if err := snapshots.FreezeMarketSnapshot(market.ID); err != nil {
		t.Fatal(err)
	}

	defaultIDs := featureNames(marketsnapshot.DefaultDslFeatures())
	staleIDs := make([]string, 0, len(defaultIDs))
	for _, id := range defaultIDs {
		if id == "gap_pct" || id == "return1" {
			continue
		}
		staleIDs = append(staleIDs, id)
	}
	now := time.Now().UTC()
	// 升级前物化的快照：没有 gap_pct，但 built_at 更新（后写入）。
	stale := &marketsnapshot.FeatureSnapshot{ID: "fs-stale-22", MarketSnapshotID: market.ID, SnapshotDate: market.SnapshotDate, FeatureIDs: staleIDs, FeatureTotal: len(staleIDs), RowsWritten: 1, LeakChecked: true, PriceAdjustment: "forward", Status: marketsnapshot.StatusReady, AsOfNs: now.UnixNano(), BuiltAt: now, Values: map[string]map[string]float64{"000001": {"close": 10.5, "open": 10.6, "ma20": 10, "amount": 100000000}}}
	stale.ContentHash, _ = marketsnapshot.ComputeFeatureContentHash(stale)
	if err := snapshots.SaveFeatureSnapshot(stale); err != nil {
		t.Fatal(err)
	}
	// 重建后的完整快照：含 gap_pct，built_at 更早。
	covering := &marketsnapshot.FeatureSnapshot{ID: "fs-covering", MarketSnapshotID: market.ID, SnapshotDate: market.SnapshotDate, FeatureIDs: defaultIDs, FeatureTotal: len(defaultIDs), RowsWritten: 1, LeakChecked: true, PriceAdjustment: "forward", Status: marketsnapshot.StatusReady, AsOfNs: now.Add(-time.Hour).UnixNano(), BuiltAt: now.Add(-time.Hour), Values: map[string]map[string]float64{"000001": {"close": 10.5, "open": 10.6, "ma20": 10, "amount": 100000000, "gap_pct": 0.05}}}
	covering.ContentHash, _ = marketsnapshot.ComputeFeatureContentHash(covering)
	if err := snapshots.SaveFeatureSnapshot(covering); err != nil {
		t.Fatal(err)
	}

	run := runGapMethodSelection(t, ctx, snapshots, methodsRepo, runs, market.ID, "")
	if run.FeatureSnapshotID != covering.ID {
		t.Fatalf("engine picked feature snapshot %s, want %s (covers gap_pct); exclusions=%+v", run.FeatureSnapshotID, covering.ID, run.Exclusions)
	}
	if len(run.Candidates) != 1 {
		t.Fatalf("expected 1 candidate from the covering snapshot, got %d; exclusions=%+v", len(run.Candidates), run.Exclusions)
	}
}

// TestSelectionEnsurerRebuildsStaleSnapshot covers the screen-click path: only
// a stale pre-gap_pct snapshot exists and no explicit feature_snapshot_id was
// requested. With a FeatureEnsurer wired, the engine must materialize a covering
// snapshot on the fly instead of returning an all-insufficient_data run.
func TestSelectionEnsurerRebuildsStaleSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := storage.New(storage.Config{Driver: "sqlite3", DSN: filepath.Join(t.TempDir(), "selection-ensure.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	snapshots, _ := marketsnapshotrepo.New(store)
	methodsRepo, _ := methodregistryrepo.New(store)
	runs, _ := selectionrepo.New(store)

	market := &marketsnapshot.MarketSnapshot{ID: "ensure-snapshot", SnapshotDate: "2024-01-02", Universe: marketsnapshot.UniverseDefinition{Name: "universe_usable"}, Market: "CN-A", PriceAdjustment: "forward", ExpectedKlineCodes: 1, ReadyKlineCodes: 1, CoveragePct: 1, Status: marketsnapshot.StatusReady, Frozen: false, UniverseMembers: []marketsnapshot.UniverseMember{{Code: "000001", Selected: true, Status: "normal"}}, Codes: []marketsnapshot.CodeStatus{{Code: "000001", UniverseMember: true, KlineLastDate: "2024-01-02", KlineRowCount: 260}}}
	market.UniverseHash = marketsnapshot.ComputeUniverseHash(market.UniverseMembers)
	market.ContentHash, _ = marketsnapshot.ComputeContentHash(market)
	if err := snapshots.SaveMarketSnapshot(market); err != nil {
		t.Fatal(err)
	}
	if err := snapshots.FreezeMarketSnapshot(market.ID); err != nil {
		t.Fatal(err)
	}
	staleIDs := []string{"amount", "close", "ma20", "open"}
	stale := &marketsnapshot.FeatureSnapshot{ID: "fs-only-stale", MarketSnapshotID: market.ID, SnapshotDate: market.SnapshotDate, FeatureIDs: staleIDs, FeatureTotal: len(staleIDs), RowsWritten: 1, LeakChecked: true, PriceAdjustment: "forward", Status: marketsnapshot.StatusReady, AsOfNs: time.Now().UnixNano(), Values: map[string]map[string]float64{"000001": {"close": 10.5, "open": 10.6, "ma20": 10, "amount": 100000000}}}
	stale.ContentHash, _ = marketsnapshot.ComputeFeatureContentHash(stale)
	if err := snapshots.SaveFeatureSnapshot(stale); err != nil {
		t.Fatal(err)
	}

	engine, err := selection.NewEngine(snapshots, methodsRepo, runs)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetFeatureEnsurer(&marketsnapshot.FeatureEnsurer{
		Builder: &marketsnapshot.Builder{Now: time.Now()}, Snapshots: snapshots, Features: stubGapFeatureEngine{},
	})
	run := runGapMethodSelectionWith(t, ctx, engine, methodsRepo, market.ID, "")
	if run.FeatureSnapshotID == stale.ID {
		t.Fatalf("ensurer returned the stale snapshot %s instead of materializing a covering one", stale.ID)
	}
	if len(run.Candidates) != 1 {
		t.Fatalf("expected 1 candidate after ensuring gap_pct, got %d; exclusions=%+v", len(run.Candidates), run.Exclusions)
	}
	ensured, err := snapshots.LoadFeatureSnapshot(run.FeatureSnapshotID, false)
	if err != nil {
		t.Fatal(err)
	}
	if missing := ensured.MissingFeatures([]string{"gap_pct"}); len(missing) > 0 {
		t.Fatalf("ensured snapshot missing %v; FeatureIDs=%v", missing, ensured.FeatureIDs)
	}
}

// runGapMethodSelection 注册跳空方法并跑一次不指定 feature_snapshot_id 的选股。
func runGapMethodSelection(t *testing.T, ctx context.Context, snapshots *marketsnapshotrepo.SQLiteRepository, methodsRepo *methodregistryrepo.SQLiteRepository, runs *selectionrepo.SQLiteRepository, marketID, featureID string) *selection.Run {
	t.Helper()
	engine, err := selection.NewEngine(snapshots, methodsRepo, runs)
	if err != nil {
		t.Fatal(err)
	}
	return runGapMethodSelectionWith(t, ctx, engine, methodsRepo, marketID, featureID)
}

func runGapMethodSelectionWith(t *testing.T, ctx context.Context, engine *selection.Engine, methodsRepo *methodregistryrepo.SQLiteRepository, marketID, featureID string) *selection.Run {
	t.Helper()
	positionPct := 0.1
	compiled, _, err := methods.Compile(&methods.Candidate{
		Name: "跳空超过2%", Universe: "universe_usable",
		Entry:          map[string]any{"type": "compare", "left": map[string]any{"type": "indicator", "indicator": "gap_pct"}, "right": map[string]any{"type": "constant", "value": 0.02}, "op": "gt"},
		HoldingMaxDays: 10, PositionMode: "pct_equity", PositionPct: &positionPct,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	method := &methodregistry.Method{ID: "method-gap", FamilyID: "family-gap", VariantID: "v1", Name: compiled.Name, Status: methodregistry.StatusVerified, Market: "A", Universe: "universe_usable", HoldingMaxDays: 10, CurrentVersion: 1, Versions: []methodregistry.MethodVersion{{ID: "method-gap-v1", Version: 1, MethodHash: compiled.ContentHash, Method: compiled, Evidence: &methodregistry.EvidenceSummary{ResultHash: "evidence-gap", SnapshotID: "validation-real", Confidence: "strong", Passable: true, OOSTrades: 120, OOSMaxDrawdown: -.1}, CreatedAt: now}}, CreatedAt: now, UpdatedAt: now}
	if err := methodsRepo.Save(ctx, method, methodregistry.AuditEvent{ID: "audit-method-gap-pref", MethodID: method.ID, From: methodregistry.StatusCandidate, To: methodregistry.StatusVerified, Action: "policy", Automatic: true, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run, err := engine.Run(ctx, selection.Request{MarketSnapshotID: marketID, FeatureSnapshotID: featureID})
	if err != nil {
		t.Fatal(err)
	}
	return run
}
