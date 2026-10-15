package methods

// SeedCandidate 是一个内置示例方法定义，附带它在产品里的用途说明。
// 它只是「编译器输入」，是否可信由验证工厂在真实冻结数据上回测决定，
// 本包不声明任何有效性。
type SeedCandidate struct {
	Key       string
	Rationale string
	Candidate *Candidate
}

// SeedCandidates 返回内置示例方法（冷启动种子）。
//
// 设计约束（与选股链路一致，避免「验证通过但选股永不触发」的假象）：
//   - 全部使用单日可判定的状态条件，不含 cross / in_window（选股只持有
//     单日冻结特征快照，无法回看历史）。
//   - 显式声明 FeatureDeps，使选股阶段能对缺失特征 fail closed。
//   - 股票池为 universe_usable，与市场快照宇宙名一致。
//   - 仓位 10% 等权，满足 critic 的单票权重硬上限。
func SeedCandidates() []SeedCandidate {
	return []SeedCandidate{
		{Key: "ma_alignment", Rationale: "均线多头排列：短中长期均线依次向上，代表趋势结构完好。", Candidate: seedMAAlignment()},
		{Key: "macd_bull", Rationale: "MACD 多头：DIF 位于 DEA 上方且柱状为正，代表中期动能偏多。", Candidate: seedMACDBull()},
		{Key: "volume_breakout", Rationale: "量价突破：收盘创 20 日新高且成交量高于 20 日均量，代表放量突破。", Candidate: seedVolumeBreakout()},
		{Key: "rsi_oversold", Rationale: "超卖反转：RSI(14) 低于 30 的经典超卖买入，RSI 修复到 50 离场的均值回归。", Candidate: seedRSIOversold()},
		{Key: "trend_pullback", Rationale: "趋势回踩：收盘在 20 日线上，且 5 日乖离率小幅为负（回踩 5 日线），顺势低吸。", Candidate: seedTrendPullback()},
		{Key: "low_vol_uptrend", Rationale: "低波动趋势：20 日波动率低于 2% 且站在 20 日线上，低波动异象叠加短期趋势过滤。", Candidate: seedLowVolUptrend()},
		{Key: "boll_lower_bounce", Rationale: "布林下轨承接：触及布林下轨但收盘收回当日高位，显示买盘承接后的均值回归。", Candidate: seedBollLowerBounce()},
		{Key: "kdj_low_uptrend", Rationale: "KDJ 低位承接：J 值小于 0 且 K 值低于 30 的极端超卖，当日收在振幅高位显示承接时介入。", Candidate: seedKDJLowUptrend()},
		{Key: "bias20_reversal", Rationale: "乖离超跌反转：20 日乖离率低于 -12% 且当日收在振幅高位，博弈超跌反弹。", Candidate: seedBias20Reversal()},
		// 高胜率专项候选（基于全市场分层验证结论：超跌承接类胜率最高，条件越极端胜率越高但交易越少）
		{Key: "bias20_deep_reversal", Rationale: "极端超跌反转：20 日乖离率低于 -18% 的恐慌盘，且当日收在振幅上 70% 以上，只做最极端的错杀反弹。", Candidate: seedBias20DeepReversal()},
		{Key: "kdj_extreme_bounce", Rationale: "极端超卖承接：KDJ J 值低于 -10 的极端超卖叠加当日强势承接（振幅上 70%）。", Candidate: seedKDJExtremeBounce()},
		{Key: "gap_up_strength", Rationale: "跳空强势：跳空高开 2% 以上且收盘守住当日上半区，动量延续。", Candidate: seedGapUpStrength()},
		{Key: "vol_dryup_pullback", Rationale: "缩量回踩：趋势向上（站上 20 日线）且回踩 5 日线、5 日量比低于 0.6（抛压枯竭）时低吸。", Candidate: seedVolDryupPullback()},
		{Key: "boll_strong_bounce", Rationale: "下轨强承接：触及布林下轨且收盘收复至当日振幅 85% 以上，承接力度更强的均值回归。", Candidate: seedBollStrongBounce()},
		{Key: "rsi6_extreme_reversal", Rationale: "短周期极端超卖：RSI(6) 低于 15 的极端超卖且当日承接（振幅上 60%），快进快出。", Candidate: seedRSI6ExtremeReversal()},
	}
}

func seedMAAlignment() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.30
	return &Candidate{
		Name:           "均线多头排列",
		Description:    "收盘价站上 ma5，且 ma5 > ma10 > ma20，趋势结构完好时入场；跌破 ma10 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"close", "ma5", "ma10", "ma20"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 30,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("gt", indicator("close"), indicator("ma5")),
				compare("gt", indicator("ma5"), indicator("ma10")),
				compare("gt", indicator("ma10"), indicator("ma20")),
			},
		},
		Exit: compare("lt", indicator("close"), indicator("ma10")),
	}
}

func seedMACDBull() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.25
	return &Candidate{
		Name:           "MACD 多头",
		Description:    "DIF 位于 DEA 上方且 MACD 柱为正时入场；DIF 跌破 DEA 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"macd_dif", "macd_dea", "macd_hist"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 25,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("gt", indicator("macd_dif"), indicator("macd_dea")),
				compare("gt", indicator("macd_hist"), constant(0)),
			},
		},
		Exit: compare("lt", indicator("macd_dif"), indicator("macd_dea")),
	}
}

func seedVolumeBreakout() *Candidate {
	pct := 0.10
	stopLoss := -0.07
	takeProfit := 0.20
	return &Candidate{
		Name:           "量价突破",
		Description:    "收盘价创 20 日新高、成交量高于 20 日均量且站上 ma20 时入场；跌破 ma10 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"close", "ma10", "ma20", "prevhigh20", "volume", "volma20"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 20,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("gt", indicator("close"), indicator("prevhigh20")),
				compare("gt", indicator("volume"), indicator("volma20")),
				compare("gt", indicator("close"), indicator("ma20")),
			},
		},
		Exit: compare("lt", indicator("close"), indicator("ma10")),
	}
}

func seedRSIOversold() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.20
	return &Candidate{
		Name:           "超卖反转",
		Description:    "RSI(14) 低于 30 的超卖状态买入，RSI 修复到 50 或触发止损/止盈离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"rsi14"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 20,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry:          compare("lt", indicator("rsi14"), constant(30)),
		Exit:           compare("gt", indicator("rsi14"), constant(50)),
	}
}

func seedTrendPullback() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.25
	return &Candidate{
		Name:           "趋势回踩",
		Description:    "收盘站在 20 日线上（趋势向上），且 5 日乖离率介于 -3% 与 0 之间（回踩至 5 日线下方但不深）时低吸；乖离率回到 +2% 或跌破 20 日线离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"close", "ma20", "bias5"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 25,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("gt", indicator("close"), indicator("ma20")),
				compare("gt", indicator("bias5"), constant(-0.03)),
				compare("lt", indicator("bias5"), constant(0)),
			},
		},
		Exit: map[string]any{
			"type": "or",
			"children": []any{
				compare("gt", indicator("bias5"), constant(0.02)),
				compare("lt", indicator("close"), indicator("ma20")),
			},
		},
	}
}

func seedLowVolUptrend() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.20
	return &Candidate{
		Name:           "低波动趋势",
		Description:    "20 日日波动率低于 2% 且收盘站在 20 日线上（低波动异象 + 短期趋势过滤）时持有；跌破 20 日线离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"volatility20", "close", "ma20"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 30,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("volatility20"), constant(0.02)),
				compare("gt", indicator("close"), indicator("ma20")),
			},
		},
		Exit: compare("lt", indicator("close"), indicator("ma20")),
	}
}

func seedBollLowerBounce() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.15
	return &Candidate{
		Name:           "布林下轨承接",
		Description:    "收盘触及布林(20,2)下轨、但收在当日振幅上 70% 以上（下影承接）时介入；回到中轨即离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"close", "boll_lower", "boll_mid", "closepos"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 15,
		HoldingMinDays: 2,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("close"), indicator("boll_lower")),
				compare("gt", indicator("closepos"), constant(0.7)),
			},
		},
		Exit: compare("gt", indicator("close"), indicator("boll_mid")),
	}
}

func seedKDJLowUptrend() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.20
	return &Candidate{
		Name:           "KDJ 低位承接",
		Description:    "KDJ J 值小于 0 且 K 值低于 30（极端超卖），且当日收在振幅上 60% 以上（承接迹象）时介入；J 值回到 80 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"kdj_j", "kdj_k", "closepos"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 20,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("kdj_j"), constant(0)),
				compare("lt", indicator("kdj_k"), constant(30)),
				compare("gt", indicator("closepos"), constant(0.6)),
			},
		},
		Exit: compare("gt", indicator("kdj_j"), constant(80)),
	}
}

func seedBias20Reversal() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.20
	return &Candidate{
		Name:           "乖离超跌反转",
		Description:    "20 日乖离率低于 -12%（深度超跌）且当日收在振幅上 60% 以上（企稳迹象）时博弈反弹；乖离率修复到 -5% 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"bias20", "closepos"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 20,
		HoldingMinDays: 2,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("bias20"), constant(-0.12)),
				compare("gt", indicator("closepos"), constant(0.6)),
			},
		},
		Exit: compare("gt", indicator("bias20"), constant(-0.05)),
	}
}

func indicator(name string) map[string]any {
	return map[string]any{"type": "indicator", "indicator": name}
}

func seedBias20DeepReversal() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.15
	return &Candidate{
		Name:           "极端超跌反转",
		Description:    "20 日乖离率低于 -18%（恐慌性错杀）且当日收在振幅上 70% 以上时博弈反弹；乖离率修复到 -8% 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"bias20", "closepos"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 15,
		HoldingMinDays: 2,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("bias20"), constant(-0.18)),
				compare("gt", indicator("closepos"), constant(0.7)),
			},
		},
		Exit: compare("gt", indicator("bias20"), constant(-0.08)),
	}
}

func seedKDJExtremeBounce() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.20
	return &Candidate{
		Name:           "极端超卖承接",
		Description:    "KDJ J 值低于 -10（极端超卖）且当日收在振幅上 70% 以上（承接迹象）时介入；J 值回到 80 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"kdj_j", "closepos"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 20,
		HoldingMinDays: 2,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("kdj_j"), constant(-10)),
				compare("gt", indicator("closepos"), constant(0.7)),
			},
		},
		Exit: compare("gt", indicator("kdj_j"), constant(80)),
	}
}

func seedGapUpStrength() *Candidate {
	pct := 0.10
	stopLoss := -0.07
	takeProfit := 0.20
	return &Candidate{
		Name:           "跳空强势",
		Description:    "跳空高开 2% 以上且收盘守住当日振幅上半区（强势未回补）时介入；跌破 5 日线离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"gap_pct", "closepos", "ma5"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 20,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("gt", indicator("gap_pct"), constant(0.02)),
				compare("gt", indicator("closepos"), constant(0.5)),
			},
		},
		Exit: compare("lt", indicator("close"), indicator("ma5")),
	}
}

func seedVolDryupPullback() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.20
	return &Candidate{
		Name:           "缩量回踩",
		Description:    "趋势向上（收盘站上 20 日线）且回踩 5 日线（乖离 -3%~0）、5 日量比低于 0.6（抛压枯竭）时低吸；乖离回到 +2% 或跌破 20 日线离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"close", "ma20", "bias5", "volratio5"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 25,
		HoldingMinDays: 3,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("gt", indicator("close"), indicator("ma20")),
				compare("gt", indicator("bias5"), constant(-0.03)),
				compare("lt", indicator("bias5"), constant(0)),
				compare("lt", indicator("volratio5"), constant(0.6)),
			},
		},
		Exit: map[string]any{
			"type": "or",
			"children": []any{
				compare("gt", indicator("bias5"), constant(0.02)),
				compare("lt", indicator("close"), indicator("ma20")),
			},
		},
	}
}

func seedBollStrongBounce() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.15
	return &Candidate{
		Name:           "下轨强承接",
		Description:    "收盘触及布林(20,2)下轨、且收在当日振幅上 85% 以上（强力承接）时介入；回到中轨即离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"close", "boll_lower", "boll_mid", "closepos"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 15,
		HoldingMinDays: 2,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("close"), indicator("boll_lower")),
				compare("gt", indicator("closepos"), constant(0.85)),
			},
		},
		Exit: compare("gt", indicator("close"), indicator("boll_mid")),
	}
}

func seedRSI6ExtremeReversal() *Candidate {
	pct := 0.10
	stopLoss := -0.08
	takeProfit := 0.15
	return &Candidate{
		Name:           "短周期极端超卖",
		Description:    "RSI(6) 低于 15 的极端超卖且当日收在振幅上 60% 以上（企稳迹象）时介入；RSI(6) 回到 45 离场。",
		SourceKind:     "structured",
		Universe:       "universe_usable",
		FeatureDeps:    []string{"rsi6", "closepos"},
		PositionMode:   "pct_equity",
		PositionPct:    &pct,
		HoldingMaxDays: 15,
		HoldingMinDays: 2,
		StopLossPct:    &stopLoss,
		TakeProfitPct:  &takeProfit,
		Entry: map[string]any{
			"type": "and",
			"children": []any{
				compare("lt", indicator("rsi6"), constant(15)),
				compare("gt", indicator("closepos"), constant(0.6)),
			},
		},
		Exit: compare("gt", indicator("rsi6"), constant(45)),
	}
}

func constant(value float64) map[string]any {
	return map[string]any{"type": "constant", "value": value}
}

func compare(op string, left, right map[string]any) map[string]any {
	return map[string]any{"type": "compare", "op": op, "left": left, "right": right}
}
