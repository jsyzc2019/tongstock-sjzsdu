package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/sjzsdu/tongstock/internal/adapter/methodregistryrepo"
	"github.com/sjzsdu/tongstock/internal/adapter/validationrepo"
	"github.com/sjzsdu/tongstock/internal/methodregistry"
	"github.com/sjzsdu/tongstock/internal/methods"
	"github.com/sjzsdu/tongstock/internal/validation"
	"github.com/spf13/cobra"
)

var (
	stratifyMethod   string
	stratifySnapshot string // 兼容保留：数据固定读活表，此参数被忽略
	stratifyGroups   int
	stratifyMaxCodes int
	stratifySplit     string
	stratifyBy        string
	stratifyEmitScope bool
	stratifyRegime    bool
)

// stratifyRegimeStat 是一个分层在某市场环境下的样本外汇总。
type stratifyRegimeStat struct {
	Regime      string  `json:"regime"`
	Trades      int     `json:"trades"`
	WinRate     float64 `json:"win_rate"`
	AvgTradeRet float64 `json:"avg_trade_return"`
}

// stratifyGroupResult 是一个分层股票池的样本外汇总。
type stratifyGroupResult struct {
	Label       string               `json:"label"`
	Codes       int                  `json:"codes"`
	Trades      int                  `json:"oos_trades"`
	WinRate     float64              `json:"oos_win_rate"`
	AvgTradeRet float64              `json:"oos_avg_trade_return"`
	SegAvgRet   float64              `json:"oos_segment_avg_return"`
	MaxDrawdown float64              `json:"oos_max_drawdown"`
	Benchmark   float64              `json:"oos_benchmark_return"`
	Excess      float64              `json:"oos_excess_return"`
	Regimes     []stratifyRegimeStat `json:"regimes,omitempty"`
}

// stratCodeBars 是分层内一只股票的全窗口日线与分层依据。
type stratCodeBars struct {
	code      string
	avgAmount float64 // 窗口日均成交额（amount 分层依据）
	avgClose  float64 // 窗口均价（市值近似 = 流通股本 × 均价）
	shares    float64 // 流通股本（stockinfo.liu_tong_gu_ben，万股）
	holders   float64 // 股东户数（finance_snapshot.GuDongRenShu）
	bars      []validation.BacktestBar
}

var methodLibraryStratifyCmd = &cobra.Command{
	Use:   "stratify",
	Short: "按历史日均成交额把股票池分层，逐层重验一个方法的样本外表现",
	Long: `分层稳健性分析：同一个方法在不同结构的股票上表现不同。
本命令把验证股票池按「全窗口历史日均成交额」升序等分为 N 层（默认 3 层），
逐层用与正式验证完全相同的回测引擎、切分方式和股票池重算样本外统计，
用于回答「该方法在哪类股票上有效、在哪类股票上失效」。

这是研究分析工具：只读活表数据与注册表，不修改方法库，不产出新的晋级证据。
用法示例：
  tongstock method library stratify --method method-9626afeb68e7b3c3`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
		defer cancel()

		store, err := openValidationStorage()
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Migrate(); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}

		repo, err := methodregistryrepo.New(store)
		if err != nil {
			return err
		}
		m, err := repo.Get(ctx, stratifyMethod)
		if err != nil {
			return fmt.Errorf("load method %s: %w", stratifyMethod, err)
		}
		compiled := currentCompiled(m)
		if compiled == nil || !compiled.IsExecutable() {
			return fmt.Errorf("method %s has no executable compiled version", stratifyMethod)
		}

		// 项目红线：直读活表全量数据。--snapshot 参数仅作兼容保留并被忽略。
		liveSource := validationrepo.NewLiveSource(store)
		barsProvider := validation.BarProvider(liveSource)
		source, err := liveSource.GetByID("")
		if err != nil {
			return err
		}
		codes, skipped, err := liveSource.ResolveUniverse(ctx, "", 30, stratifyMaxCodes)
		if err != nil {
			return fmt.Errorf("resolve live universe: %w", err)
		}
		if len(codes) < 5 {
			return fmt.Errorf("universe too small: %d codes (need >= 5)", len(codes))
		}
		_ = skipped

		// 分层依据数据：流通股本（stockinfo）与股东户数（finance_snapshot）。
		sharesMap := map[string]float64{}
		if rows, err := store.DB().Query(`SELECT code, liu_tong_gu_ben FROM stockinfo WHERE liu_tong_gu_ben > 0`); err == nil {
			for rows.Next() {
				var c string
				var v float64
				if rows.Scan(&c, &v) == nil {
					sharesMap[c] = v
				}
			}
			rows.Close()
		}
		holdersMap := map[string]float64{}
		if rows, err := store.DB().Query(`SELECT code, payload FROM finance_snapshot`); err == nil {
			for rows.Next() {
				var c, raw string
				if rows.Scan(&c, &raw) != nil {
					continue
				}
				var payload struct {
					GuDongRenShu float64 `json:"GuDongRenShu"`
				}
				if json.Unmarshal([]byte(raw), &payload) == nil && payload.GuDongRenShu > 0 {
					holdersMap[c] = payload.GuDongRenShu
				}
			}
			rows.Close()
		}

		// 加载全窗口真实日线，并统计每只股票的分层指标输入。
		loaded := make([]stratCodeBars, 0, len(codes))
		dateSet := map[string]struct{}{}
		for _, code := range codes {
			bars, err := barsProvider.LoadBars(ctx, "", code, source.DateRange.Start, source.DateRange.End)
			if err != nil {
				return fmt.Errorf("load bars %s: %w", code, err)
			}
			var sum, closeSum float64
			for _, b := range bars {
				sum += b.Amount
				closeSum += b.Close
				dateSet[b.Date] = struct{}{}
			}
			loaded = append(loaded, stratCodeBars{
				code: code, avgAmount: sum / float64(len(bars)),
				avgClose: closeSum / float64(len(bars)),
				shares:   sharesMap[code], holders: holdersMap[code],
				bars: bars,
			})
		}

		dates := make([]string, 0, len(dateSet))
		for d := range dateSet {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		plan, err := validation.PlanSegments(dates, stratifySplit)
		if err != nil {
			return fmt.Errorf("plan segments: %w", err)
		}

		cfg := validation.DefaultBacktestConfig()
		switch compiled.Position.Mode {
		case "pct_equity":
			if compiled.Position.PctEquity == nil || *compiled.Position.PctEquity <= 0 || *compiled.Position.PctEquity > 1 {
				return fmt.Errorf("invalid pct_equity position rule")
			}
			cfg.PositionPct = *compiled.Position.PctEquity
		case "":
			// 与验证工厂一致：旧制品无显式仓位时用默认满仓。
		default:
			return fmt.Errorf("unsupported position mode %q", compiled.Position.Mode)
		}

		// 按分层维度分组（升序均分；cap_holders 为市值×户均 3×3 组合）。
		groups, labels, assignSkip, err := stratifyAssign(loaded, stratifyBy, stratifyGroups)
		if err != nil {
			return err
		}
		if assignSkip > 0 {
			fmt.Printf("（%d 只缺少分层依据数据，已从分层中排除）\n\n", assignSkip)
		}

		// 市场环境标注：指数日线不可用时用池内等权指数代理大盘（多头/空头）。
		var regimeMap map[string]string
		if stratifyRegime {
			regimeMap = buildRegimeMap(dates, loaded)
		}

		results := make([]stratifyGroupResult, 0, len(groups)+1)
		for g := range groups {
			res := runStratifyGroup(ctx, compiled, plan.Segments, groups[g], cfg, regimeMap)
			res.Label = labels[g]
			results = append(results, res)
		}
		// 全池对照行（混合结果）。
		all := runStratifyGroup(ctx, compiled, plan.Segments, loaded, cfg, regimeMap)
		all.Label = "全池对照"
		results = append(results, all)

		window := oosWindow(plan.Segments)
		splitName := stratifySplit
		if splitName == "" {
			splitName = "walk_forward"
		}
		fmt.Printf("方法: %s (%s)\n数据源: %s（活表全量） | 股票池: %d 只 | 分层维度: %s | 数据窗口: %s ~ %s | 样本外窗口: %s ~ %s | 切分: %s\n\n",
			m.Name, m.ID, source.ID, len(loaded), stratifyBy, dates[0], dates[len(dates)-1], window[0], window[1],
			splitName)
		fmt.Printf("%-22s %6s %8s %8s %10s %10s %8s %10s %10s\n",
			"分层", "代码数", "OOS交易", "胜率", "单笔均收", "段均收益", "回撤", "基准收益", "超额")
		for _, r := range results {
			fmt.Printf("%-22s %6d %8d %7.1f%% %9.2f%% %9.2f%% %7.2f%% %9.2f%% %9.2f%%\n",
				r.Label, r.Codes, r.Trades, r.WinRate*100,
				r.AvgTradeRet*100, r.SegAvgRet*100, r.MaxDrawdown*100, r.Benchmark*100, r.Excess*100)
		}
		fmt.Println("\n说明: 单笔均收=样本外交易平均收益率; 段均收益=各样本外窗口总收益的均值(与验证证据口径一致);")
		fmt.Println("      基准=该层等权买入持有; 超额=段均收益-基准。分层数值差异大 → 方法对股票结构敏感, 应限定适用层。")

		if stratifyEmitScope {
			emitScopeSuggestion(stratifyBy, groups, labels, results[:len(groups)])
		}

		if stratifyRegime {
			fmt.Println("\n市场环境标注（池内等权指数 vs 其60日均线；指数日线不可用，用池内等权代理大盘）:")
			fmt.Printf("%-22s %6s %8s %8s %10s\n", "分层", "环境", "OOS交易", "胜率", "单笔均收")
			for _, r := range results {
				for _, rs := range r.Regimes {
					fmt.Printf("%-22s %6s %8d %7.1f%% %9.2f%%\n",
						r.Label, rs.Regime, rs.Trades, rs.WinRate*100, rs.AvgTradeRet*100)
				}
			}
		}
		return nil
	},
}

// emitScopeSuggestion 把表现最好的分层（单笔均收最高且有交易）固化为方法 scope
// 条件，输出可直接粘进方法候选 JSON 的建议。排序用单笔均收而非超额：牛市窗口
// 超额普遍为负（跑不赢等权持有），单笔均收才反映方法本身质量。
// 数值基于窗口近似（市值=窗口均价×流通股本，户均=流通股本/股东户数），
// 与选股端当前值口径略有差异，用作区间边界足够。
func emitScopeSuggestion(by string, groups [][]stratCodeBars, labels []string, results []stratifyGroupResult) {
	best := -1
	for i, r := range results {
		if r.Trades <= 0 {
			continue
		}
		if best < 0 || r.AvgTradeRet > results[best].AvgTradeRet {
			best = i
		}
	}
	if best < 0 {
		fmt.Println("\n建议 scope: 所有分层均无样本外交易，无法固化。")
		return
	}
	members := groups[best]
	scope := map[string]any{}
	if by == "amount" {
		fmt.Println("\n建议 scope: amount 分层无法直接固化为 scope 条件（scope 不支持成交额区间），请改用 --by market_cap/holders/cap_holders。")
		return
	}
	if by == "market_cap" || by == "cap_holders" {
		lo, hi := metricBounds(members, func(cb stratCodeBars) float64 { return cb.avgClose * cb.shares }) // 万元
		if lo > 0 {
			scope["market_cap_min"] = roundScope(lo / 1e4) // 万元 → 亿元
			scope["market_cap_max"] = roundScope(hi / 1e4)
		}
	}
	if by == "holders" || by == "cap_holders" {
		lo, hi := metricBounds(members, func(cb stratCodeBars) float64 {
			if cb.holders <= 0 {
				return -1
			}
			return cb.shares / cb.holders * 10000 // 万股/户 → 股/户
		})
		if lo > 0 {
			scope["avg_shares_per_holder_min"] = roundScope(lo)
			scope["avg_shares_per_holder_max"] = roundScope(hi)
		}
	}
	raw, _ := json.MarshalIndent(scope, "", "  ")
	fmt.Printf("\n建议 scope（固化最优层「%s」，%d 笔样本外交易，单笔均收 %.2f%%、胜率 %.1f%%）:\n%s\n",
		labels[best], results[best].Trades, results[best].AvgTradeRet*100, results[best].WinRate*100, raw)
	fmt.Println("提示: 数值为窗口近似边界，新建方法时把上述键加入候选 JSON 的 scope，或交给 AI 生成时带上。")
}

// metricBounds 返回一组成员某指标的最小/最大值（剔除 -1 缺失值）。
func metricBounds(members []stratCodeBars, key func(stratCodeBars) float64) (float64, float64) {
	lo, hi := -1.0, -1.0
	for _, cb := range members {
		v := key(cb)
		if v < 0 {
			continue
		}
		if lo < 0 || v < lo {
			lo = v
		}
		if hi < 0 || v > hi {
			hi = v
		}
	}
	return lo, hi
}

func roundScope(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// runStratifyGroup 对一组股票逐段回测并聚合样本外统计。
// regimeMap 为空时跳过市场环境标注。
func runStratifyGroup(ctx context.Context, compiled *methods.CompiledMethod, specs []validation.SegmentSpec, members []stratCodeBars, cfg validation.BacktestConfig, regimeMap map[string]string) stratifyGroupResult {
	var segResults []validation.SegmentResult
	for _, cb := range members {
		for _, spec := range specs {
			segBars := barsInStratifyRange(cb.bars, spec.DateStart, spec.DateEnd)
			if len(segBars) < 2 {
				continue
			}
			result, err := validation.RunBacktest(ctx, compiled, segBars, cfg)
			if err != nil {
				continue
			}
			segResults = append(segResults, validation.SegmentResult{
				Segment: spec.Name, Code: cb.code,
				Start: spec.DateStart, End: spec.DateEnd,
				Stats: result.Stats, Trades: result.Trades,
			})
		}
	}
	stats := validation.AggregateOosStats(segResults)
	stats.BenchmarkReturn = stratifyBenchmark(specs, members)
	stats.ExcessReturn = stats.TotalReturn - stats.BenchmarkReturn

	var trades []validation.TradeRecord
	for _, s := range segResults {
		if s.Segment != "train" && s.Segment != "valid" {
			trades = append(trades, s.Trades...)
		}
	}
	winRate, avgTrade := 0.0, 0.0
	if len(trades) > 0 {
		wins, sum := 0, 0.0
		for _, t := range trades {
			sum += t.ReturnPct
			if t.ReturnPct > 0 {
				wins++
			}
		}
		winRate = float64(wins) / float64(len(trades))
		avgTrade = sum / float64(len(trades))
	}
	// 市场环境标注：按买入执行日所处的池内等权指数环境聚合。
	var regimes []stratifyRegimeStat
	if len(regimeMap) > 0 && len(trades) > 0 {
		agg := map[string]*stratifyRegimeStat{}
		for _, t := range trades {
			regime := regimeMap[t.BuyExecutionDate]
			if regime == "" {
				regime = regimeMap[t.BuySignalDate]
			}
			if regime == "" {
				regime = "未知"
			}
			rs := agg[regime]
			if rs == nil {
				rs = &stratifyRegimeStat{Regime: regime}
				agg[regime] = rs
			}
			rs.Trades++
			rs.AvgTradeRet += t.ReturnPct
			if t.ReturnPct > 0 {
				rs.WinRate++
			}
		}
		names := make([]string, 0, len(agg))
		for name := range agg {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rs := agg[name]
			rs.WinRate /= float64(rs.Trades)
			rs.AvgTradeRet /= float64(rs.Trades)
			regimes = append(regimes, *rs)
		}
	}
	return stratifyGroupResult{
		Codes: len(members), Trades: len(trades),
		WinRate: winRate, AvgTradeRet: avgTrade,
		SegAvgRet: stats.TotalReturn, MaxDrawdown: stats.MaxDrawdown,
		Benchmark: stats.BenchmarkReturn, Excess: stats.ExcessReturn,
		Regimes: regimes,
	}
}

// buildRegimeMap 用池内等权指数（全体成员日收益均值复利累计）与其 60 日均线
// 把每个交易日标注为「多头/空头」。指数日线（999999 等）本地只有零星数据，
// 池内等权代理与验证基准口径一致，无需额外同步。
func buildRegimeMap(dates []string, members []stratCodeBars) map[string]string {
	sums := map[string]float64{}
	counts := map[string]int{}
	for _, cb := range members {
		for i := 1; i < len(cb.bars); i++ {
			prev := cb.bars[i-1].Close
			if prev <= 0 {
				continue
			}
			sums[cb.bars[i].Date] += (cb.bars[i].Close - prev) / prev
			counts[cb.bars[i].Date]++
		}
	}
	valid := make([]string, 0, len(dates))
	idx := make([]float64, 0, len(dates))
	cum := 1.0
	for _, d := range dates {
		if counts[d] == 0 {
			continue
		}
		cum *= 1 + sums[d]/float64(counts[d])
		valid = append(valid, d)
		idx = append(idx, cum)
	}
	out := map[string]string{}
	for i, d := range valid {
		if i < 60 {
			out[d] = "未知"
			continue
		}
		var ma float64
		for j := i - 59; j <= i; j++ {
			ma += idx[j]
		}
		ma /= 60
		if idx[i] >= ma {
			out[d] = "多头"
		} else {
			out[d] = "空头"
		}
	}
	return out
}

// stratifyBenchmark 计算一组成员在样本外窗口的等权买入持有收益：
// 每个窗口先算横截面日均收益，再复利累计，最后取窗口均值（与验证工厂口径一致）。
func stratifyBenchmark(specs []validation.SegmentSpec, members []stratCodeBars) float64 {
	total, windows := 0.0, 0
	for _, spec := range specs {
		if spec.Name == "train" || spec.Name == "valid" {
			continue
		}
		sums := map[string]float64{}
		counts := map[string]int{}
		for _, cb := range members {
			segBars := barsInStratifyRange(cb.bars, spec.DateStart, spec.DateEnd)
			for i := 1; i < len(segBars); i++ {
				prev := segBars[i-1].Close
				if prev <= 0 {
					continue
				}
				sums[segBars[i].Date] += (segBars[i].Close - prev) / prev
				counts[segBars[i].Date]++
			}
		}
		cum := 1.0
		days := 0
		for date, count := range counts {
			if count == 0 {
				continue
			}
			cum *= 1 + sums[date]/float64(count)
			days++
		}
		if days == 0 {
			continue
		}
		total += cum - 1
		windows++
	}
	if windows == 0 {
		return 0
	}
	return total / float64(windows)
}

// stratifyAssign 按分层维度把股票分组，返回各组、各组和标签及因缺数据排除的数量。
// 1D 模式（amount/market_cap/holders）：按指标升序均分为 n 组；
// 2D 模式（cap_holders）：市值三分 × 户均三分 = 9 组，先按市值再按户均排列。
func stratifyAssign(loaded []stratCodeBars, by string, n int) ([][]stratCodeBars, []string, int, error) {
	metric := func(cb stratCodeBars) (float64, float64, bool, bool) {
		// 返回 (市值指标, 户均指标, 有市值, 有户均)。
		cap := cb.avgClose * cb.shares
		hold := 0.0
		if cb.holders > 0 {
			hold = cb.shares / cb.holders // 户均流通股数：越高越筹码集中
		}
		return cap, hold, cb.shares > 0, cb.holders > 0
	}

	switch by {
	case "", "amount":
		if n < 2 {
			n = 2
		}
		sorted := append([]stratCodeBars(nil), loaded...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].avgAmount < sorted[j].avgAmount })
		return equalSplit(sorted, n), amountLabels(n), 0, nil
	case "market_cap":
		if n < 2 {
			n = 2
		}
		sorted, skip := sortByMetric(loaded, func(cb stratCodeBars) float64 {
			cap, _, ok, _ := metric(cb)
			if !ok {
				return -1
			}
			return cap
		})
		return equalSplit(sorted, n), amountLabelsDim(n, "市值"), skip, nil
	case "holders":
		if n < 2 {
			n = 2
		}
		sorted, skip := sortByMetric(loaded, func(cb stratCodeBars) float64 {
			_, hold, _, ok := metric(cb)
			if !ok {
				return -1
			}
			return hold
		})
		return equalSplit(sorted, n), amountLabelsDim(n, "户均持股"), skip, nil
	case "cap_holders":
		caps := make([]stratCodeBars, 0, len(loaded))
		for _, cb := range loaded {
			if cap, _, ok, _ := metric(cb); ok && cap >= 0 {
				caps = append(caps, cb)
			}
		}
		if len(caps) < 9 {
			return nil, nil, 0, fmt.Errorf("cap_holders 需要至少 9 只有完整数据的股票，当前 %d", len(caps))
		}
		// 户均维度需同时有市值与户数。
		withHold := make([]stratCodeBars, 0, len(caps))
		for _, cb := range caps {
			if _, _, ok, okH := metric(cb); ok && okH {
				withHold = append(withHold, cb)
			}
		}
		skip := len(loaded) - len(withHold)
		// 市值三分位数阈值。
		byCap, _ := sortByMetric(withHold, func(cb stratCodeBars) float64 {
			cap, _, _, _ := metric(cb)
			return cap
		})
		capT := tercileThresholds(len(byCap))
		thresholdCap := []float64{
			(func() float64 { cap, _, _, _ := metric(byCap[capT]); return cap })(),
			(func() float64 { cap, _, _, _ := metric(byCap[2*capT]); return cap })(),
		}
		// 户均三分位数阈值。
		byHold, _ := sortByMetric(withHold, func(cb stratCodeBars) float64 {
			_, hold, _, _ := metric(cb)
			return hold
		})
		holdT := tercileThresholds(len(byHold))
		thresholdHold := []float64{
			(func() float64 { _, hold, _, _ := metric(byHold[holdT]); return hold })(),
			(func() float64 { _, hold, _, _ := metric(byHold[2*holdT]); return hold })(),
		}
		cells := make([][]stratCodeBars, 9)
		for _, cb := range withHold {
			cap, hold, _, _ := metric(cb)
			ct, ht := 0, 0
			if cap > thresholdCap[0] {
				ct = 1
			}
			if cap > thresholdCap[1] {
				ct = 2
			}
			if hold > thresholdHold[0] {
				ht = 1
			}
			if hold > thresholdHold[1] {
				ht = 2
			}
			cells[ct*3+ht] = append(cells[ct*3+ht], cb)
		}
		capNames := []string{"低", "中", "高"}
		labels := make([]string, 0, 9)
		for ct := 0; ct < 3; ct++ {
			for ht := 0; ht < 3; ht++ {
				labels = append(labels, fmt.Sprintf("市值%s·户均%s", capNames[ct], capNames[ht]))
			}
		}
		return cells, labels, skip, nil
	default:
		return nil, nil, 0, fmt.Errorf("未知的分层维度 %q（可选: amount / market_cap / holders / cap_holders）", by)
	}
}

// equalSplit 升序均分，前余数个组各多拿一只。
func equalSplit(sorted []stratCodeBars, n int) [][]stratCodeBars {
	base, rem := len(sorted)/n, len(sorted)%n
	groups := make([][]stratCodeBars, 0, n)
	for g := 0; g < n; g++ {
		size := base
		if g < rem {
			size++
		}
		start := g*base + minInt(g, rem)
		groups = append(groups, sorted[start:start+size])
	}
	return groups
}

// sortByMetric 按指标升序排序；指标为 -1（缺数据）的股票被排除，返回排除数。
func sortByMetric(loaded []stratCodeBars, key func(stratCodeBars) float64) ([]stratCodeBars, int) {
	kept := make([]stratCodeBars, 0, len(loaded))
	skip := 0
	for _, cb := range loaded {
		if key(cb) < 0 {
			skip++
			continue
		}
		kept = append(kept, cb)
	}
	sort.Slice(kept, func(i, j int) bool { return key(kept[i]) < key(kept[j]) })
	return kept, skip
}

// tercileThresholds 返回三分位边界索引（组大小 = 总数/3）。
func tercileThresholds(total int) int {
	if total < 3 {
		return 0
	}
	return total / 3
}

// amountLabels 生成「低/中/高成交额层」标签。
func amountLabels(n int) []string {
	labels := make([]string, 0, n)
	for i := 0; i < n; i++ {
		labels = append(labels, tierLabel(i, n, "成交额"))
	}
	return labels
}

// amountLabelsDim 生成指定维度的「低/中/高」标签。
func amountLabelsDim(n int, dim string) []string {
	labels := make([]string, 0, n)
	for i := 0; i < n; i++ {
		labels = append(labels, tierLabel(i, n, dim))
	}
	return labels
}

func tierLabel(index, total int, dim string) string {
	if index == 0 {
		return "低" + dim + "层"
	}
	if index == total-1 {
		return "高" + dim + "层"
	}
	return fmt.Sprintf("中%s层%d", dim, index)
}

// barsInStratifyRange 返回 [start,end] 闭区间内 K 线（升序输入）。
func barsInStratifyRange(bars []validation.BacktestBar, start, end string) []validation.BacktestBar {
	lo := sort.Search(len(bars), func(i int) bool { return bars[i].Date >= start })
	hi := sort.Search(len(bars), func(i int) bool { return bars[i].Date > end })
	if lo >= hi {
		return nil
	}
	return bars[lo:hi]
}

// oosWindow 返回样本外窗口的日期范围（用于表头展示）。
func oosWindow(specs []validation.SegmentSpec) [2]string {
	start, end := "", ""
	for _, s := range specs {
		if s.Name == "train" || s.Name == "valid" {
			continue
		}
		if start == "" || s.DateStart < start {
			start = s.DateStart
		}
		if end == "" || s.DateEnd > end {
			end = s.DateEnd
		}
	}
	return [2]string{start, end}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// currentCompiled 取注册方法的当前生效编译版本。
func currentCompiled(m *methodregistry.Method) *methods.CompiledMethod {
	for _, v := range m.Versions {
		if v.Version == m.CurrentVersion && v.Method != nil {
			return v.Method
		}
	}
	if n := len(m.Versions); n > 0 {
		return m.Versions[n-1].Method
	}
	return nil
}

func init() {
	methodLibraryStratifyCmd.Flags().StringVar(&stratifyMethod, "method", "", "方法 ID（必填）")
	methodLibraryStratifyCmd.Flags().StringVar(&stratifySnapshot, "snapshot", "", "（已废弃，兼容保留并被忽略）数据固定读活表全量")
	methodLibraryStratifyCmd.Flags().IntVar(&stratifyGroups, "groups", 3, "分层组数")
	methodLibraryStratifyCmd.Flags().IntVar(&stratifyMaxCodes, "max-codes", 0, "股票池解析上限（<=0 表示不设上限，使用全部合格股票）")
	methodLibraryStratifyCmd.Flags().StringVar(&stratifyBy, "by", "market_cap", "分层维度: market_cap（流通市值）/ holders（户均持股）/ cap_holders（市值×户均 3×3）/ amount（成交额）")
	methodLibraryStratifyCmd.Flags().StringVar(&stratifySplit, "split", "", "切分类型: fixed / walk_forward（空=walk_forward）")
	methodLibraryStratifyCmd.Flags().BoolVar(&stratifyEmitScope, "emit-scope", false, "把最优层固化为方法 scope 条件并输出建议 JSON")
	methodLibraryStratifyCmd.Flags().BoolVar(&stratifyRegime, "regime", true, "输出市场环境标注（池内等权指数 vs 60日均线 的多头/空头分段统计）")
	_ = methodLibraryStratifyCmd.MarkFlagRequired("method")
	methodLibraryCmd.AddCommand(methodLibraryStratifyCmd)
}
