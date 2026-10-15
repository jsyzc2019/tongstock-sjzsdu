package validation

// 方法 scope 的股票级求值（验证侧）。
//
// 设计动机（2026-10-13 换手率 entry 过滤实验的结论）：stratify 分层的维度是
// 股票级静态特征（全窗口平均换手率 / 市值 / 户均持股），把它们翻译成 entry
// 的逐 bar 条件属于口径错位，实测会把方法拖到 rejected。正确做法是让验证
// 工厂在逐 code 回测前按 scope 过滤股票池——与 stratify 分层口径完全一致，
// "把股票池做精确"才能真正反映到验证结果里。
//
// fail-closed 语义与 selection.ResolveScope 一致：
//   - scope 要求某条件而该股票的事实缺失 → 跳过该股票（skipped，原因可见）；
//   - scope 要求市值/户均而 Facts provider 未装配 → 整体报错，不静默放行。
//
// 已知近似（与 stratify 相同，诚实记录）：流通股本/股东户数取当前最新值，
// 无 point-in-time 序列；市值 = 流通股本 × 窗口均价（送转会高估历史市值）。

import (
	"context"
	"fmt"

	"github.com/sjzsdu/tongstock/internal/methods"
)

// scopeNeedsFacts 返回 scope 是否要求股票级事实条件。
func scopeNeedsFacts(scope methods.Scope) (needTurnover, needCap, needHolder bool) {
	needTurnover = scope.AvgTurnoverMin != nil || scope.AvgTurnoverMax != nil
	needCap = scope.MarketCapMin != nil || scope.MarketCapMax != nil
	needHolder = scope.AvgSharesPerHolderMin != nil || scope.AvgSharesPerHolderMax != nil
	return
}

// scopeFacts 汇总单只股票的 scope 判定事实。
type scopeFacts struct {
	avgTurnover float64 // 窗口平均换手率（%），TurnoverRate>0 的 bar 均值；0=未知
	marketCapYi float64 // 流通市值（亿元）= 流通股本 × 窗口均价 / 1e8；0=未知
	perHolder   float64 // 户均持股（股/户）；0=未知
}

// computeScopeFacts 从窗口 bars 与 facts provider 计算股票级事实。
// provider 只在 scope 需要市值/户均时被调用（换手率从 bars 直接算）。
func computeScopeFacts(ctx context.Context, scope methods.Scope, bars []BacktestBar, provider StockFactsProvider) (scopeFacts, error) {
	needTurnover, needCap, needHolder := scopeNeedsFacts(scope)
	var f scopeFacts

	if needTurnover {
		sum, n := 0.0, 0
		for _, b := range bars {
			if b.TurnoverRate > 0 {
				sum += b.TurnoverRate
				n++
			}
		}
		if n > 0 {
			f.avgTurnover = sum / float64(n)
		}
	}

	if needCap || needHolder {
		if provider == nil {
			return f, fmt.Errorf("method scope requires market cap / holders facts but no facts provider wired: fail closed")
		}
		sf, err := provider.StockFacts(ctx, bars[0].Code)
		if err != nil {
			return f, fmt.Errorf("load stock facts: %w", err)
		}
		if needCap && sf.FloatShares > 0 {
			sumClose, n := 0.0, 0
			for _, b := range bars {
				if b.Close > 0 {
					sumClose += b.Close
					n++
				}
			}
			if n > 0 {
				f.marketCapYi = sf.FloatShares * (sumClose / float64(n)) / 1e8
			}
		}
		if needHolder && sf.FloatShares > 0 && sf.HolderCount > 0 {
			f.perHolder = sf.FloatShares / sf.HolderCount
		}
	}
	return f, nil
}

// scopeSkipReason 判定一只股票是否落在方法 scope 之外。
// 返回空字符串表示通过；非空为跳过原因（写入 SkippedCode.Reason）。
func scopeSkipReason(scope methods.Scope, f scopeFacts) string {
	if scope.AvgTurnoverMin != nil || scope.AvgTurnoverMax != nil {
		if f.avgTurnover <= 0 {
			return "scope_data_unavailable: window avg turnover unknown (no float shares?)"
		}
		if scope.AvgTurnoverMin != nil && f.avgTurnover < *scope.AvgTurnoverMin {
			return fmt.Sprintf("scope_excluded: avg turnover %.4f%% < %.4f%%", f.avgTurnover, *scope.AvgTurnoverMin)
		}
		if scope.AvgTurnoverMax != nil && f.avgTurnover > *scope.AvgTurnoverMax {
			return fmt.Sprintf("scope_excluded: avg turnover %.4f%% > %.4f%%", f.avgTurnover, *scope.AvgTurnoverMax)
		}
	}
	if scope.MarketCapMin != nil || scope.MarketCapMax != nil {
		if f.marketCapYi <= 0 {
			return "scope_data_unavailable: market cap unknown (no float shares?)"
		}
		if scope.MarketCapMin != nil && f.marketCapYi < *scope.MarketCapMin {
			return fmt.Sprintf("scope_excluded: market cap %.2f亿 < %.2f亿", f.marketCapYi, *scope.MarketCapMin)
		}
		if scope.MarketCapMax != nil && f.marketCapYi > *scope.MarketCapMax {
			return fmt.Sprintf("scope_excluded: market cap %.2f亿 > %.2f亿", f.marketCapYi, *scope.MarketCapMax)
		}
	}
	if scope.AvgSharesPerHolderMin != nil || scope.AvgSharesPerHolderMax != nil {
		if f.perHolder <= 0 {
			return "scope_data_unavailable: avg shares per holder unknown (no holder count?)"
		}
		if scope.AvgSharesPerHolderMin != nil && f.perHolder < *scope.AvgSharesPerHolderMin {
			return fmt.Sprintf("scope_excluded: avg shares per holder %.0f < %.0f", f.perHolder, *scope.AvgSharesPerHolderMin)
		}
		if scope.AvgSharesPerHolderMax != nil && f.perHolder > *scope.AvgSharesPerHolderMax {
			return fmt.Sprintf("scope_excluded: avg shares per holder %.0f > %.0f", f.perHolder, *scope.AvgSharesPerHolderMax)
		}
	}
	return ""
}
