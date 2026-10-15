package selection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sjzsdu/tongstock/internal/marketsnapshot"
	"github.com/sjzsdu/tongstock/internal/methodregistry"
	"github.com/sjzsdu/tongstock/internal/methods"
)

// PoolEntry 是方法股票池里一只股票的适用性判定结果。
type PoolEntry struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Board string `json:"board,omitempty"`
	// InScope 表示该股票落在方法 scope（股票池/市值/户均/ST 等）内。
	InScope bool `json:"in_scope"`
	// ReasonCode/Detail 是 scope 之外或数据缺失时的排除原因（InScope=true 时为空）。
	ReasonCode string `json:"reason_code,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// Triggered 表示在池内且当日入场规则命中（仅对 InScope 股票有意义）。
	Triggered bool `json:"triggered"`
	// 现值快照（可能缺失，0=无数据）。
	MarketCap          float64 `json:"market_cap,omitempty"`            // 流通市值（亿元）
	AvgSharesPerHolder float64 `json:"avg_shares_per_holder,omitempty"` // 户均持股（股/户）
	AvgTurnover        float64 `json:"avg_turnover,omitempty"`          // 近120日平均换手率（%）
	Close              float64 `json:"close,omitempty"`
}

// MethodPool 是一个方法在给定快照日适用股票池的全量判定。
type MethodPool struct {
	MethodID          string      `json:"method_id"`
	SnapshotID        string      `json:"snapshot_id"`
	FeatureSnapshotID string      `json:"feature_snapshot_id"`
	SnapshotDate      string      `json:"snapshot_date"`
	Total             int         `json:"total"`
	InScopeCount      int         `json:"in_scope_count"`
	TriggeredCount    int         `json:"triggered_count"`
	Entries           []PoolEntry `json:"entries"`
}

// MethodPool 按当前冻结快照逐只解析方法的 scope，并给出池内股票当日是否触发
// 入场规则。这是只读诊断视图：不落库、不参与 run 缓存。允许查看任意方法
// （含 rejected）的池，因为「这个方法圈了哪些股票」与证据状态无关。
func (e *Engine) MethodPool(ctx context.Context, req Request) (*MethodPool, error) {
	if strings.TrimSpace(req.MarketSnapshotID) == "" {
		return nil, fmt.Errorf("market_snapshot_id is required")
	}
	market, err := e.snapshots.LoadMarketSnapshot(req.MarketSnapshotID, true)
	if err != nil {
		return nil, err
	}
	if !market.IsReady() {
		return nil, fmt.Errorf("market snapshot %s is not frozen and ready", market.ID)
	}
	feature, err := e.resolveFeature(ctx, req, market)
	if err != nil {
		return nil, err
	}
	if feature.MarketSnapshotID != market.ID || feature.Status != marketsnapshot.StatusReady || !feature.LeakChecked || feature.SnapshotDate != market.SnapshotDate {
		return nil, fmt.Errorf("feature snapshot %s is not ready, leak-checked and bound to market snapshot %s", feature.ID, market.ID)
	}
	if len(req.MethodIDs) != 1 {
		return nil, fmt.Errorf("method pool requires exactly one method id")
	}
	found, err := e.methods.Query(ctx, methodregistry.Query{IDs: req.MethodIDs, Limit: 10})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("method %s not found", req.MethodIDs[0])
	}
	m := found[0]
	v := currentVersion(m)
	if v == nil || v.Method == nil || !v.Method.IsExecutable() {
		return nil, fmt.Errorf("method %s has no executable compiled version", m.ID)
	}
	compiled := v.Method

	memberByCode := map[string]*marketsnapshot.UniverseMember{}
	for i := range market.UniverseMembers {
		memberByCode[market.UniverseMembers[i].Code] = &market.UniverseMembers[i]
	}
	// 快照持久化不携带 name，重载后 UniverseMember.Name 为空；注入的补全器
	// 用 stockinfo 的当前名称填展示字段（仅展示，不参与 scope 判定）。
	names := map[string]string{}
	if e.poolNamer != nil {
		codes := make([]string, 0, len(memberByCode))
		for code := range memberByCode {
			codes = append(codes, code)
		}
		names = e.poolNamer(codes)
	}

	pool := &MethodPool{
		MethodID: m.ID, SnapshotID: market.ID, FeatureSnapshotID: feature.ID,
		SnapshotDate: market.SnapshotDate, Entries: []PoolEntry{},
	}
	codes := selectedCodes(market, feature)
	for _, code := range codes {
		values := feature.Values[code]
		entry := PoolEntry{Code: code}
		if member := memberByCode[code]; member != nil {
			entry.Name = member.Name
			entry.Board = member.Board
		}
		if entry.Name == "" {
			entry.Name = names[code]
		}
		entry.MarketCap = values["market_cap"]
		entry.AvgSharesPerHolder = values["avg_shares_per_holder"]
		entry.AvgTurnover = values["avg_turnover"]
		entry.Close = values["close"]

		inScope, reason, detail := ResolveScope(compiled, market, code, values)
		if !inScope {
			entry.ReasonCode, entry.Detail = reason, detail
			pool.Entries = append(pool.Entries, entry)
			continue
		}
		pool.InScopeCount++
		entry.InScope = true
		// 池内股票再判定当日是否触发入场规则（与 Run 相同的 fail-closed 门）。
		deps := compiled.Scope.FeatureDeps
		if refs := methods.ReferencedIndicators(compiled.EntryRule, compiled.ExitRule, compiled.InvalidRule); len(refs) > 0 {
			deps = append(append([]string{}, deps...), refs...)
		}
		if missing := missingFeatures(deps, values); len(missing) > 0 {
			entry.Detail = "missing features: " + strings.Join(missing, ",")
			pool.Entries = append(pool.Entries, entry)
			continue
		}
		resolved := withResolvedRanks(compiled, code, feature.Values)
		bar := barFromFeatures(code, market.SnapshotDate, values)
		result, execErr := resolved.Entry(bar, []methods.Bar{bar})
		if execErr != nil {
			entry.Detail = "execution_failed: " + execErr.Error()
			pool.Entries = append(pool.Entries, entry)
			continue
		}
		entry.Triggered = result.Matched
		if entry.Triggered {
			pool.TriggeredCount++
		}
		pool.Entries = append(pool.Entries, entry)
	}
	pool.Total = len(pool.Entries)
	sort.SliceStable(pool.Entries, func(i, j int) bool {
		// 池内触发 > 池内未触发 > 池外；同组按代码升序，保证输出稳定。
		ai, bj := rankEntry(pool.Entries[i]), rankEntry(pool.Entries[j])
		if ai != bj {
			return ai < bj
		}
		return pool.Entries[i].Code < pool.Entries[j].Code
	})
	return pool, nil
}

func rankEntry(e PoolEntry) int {
	switch {
	case e.InScope && e.Triggered:
		return 0
	case e.InScope:
		return 1
	default:
		return 2
	}
}
