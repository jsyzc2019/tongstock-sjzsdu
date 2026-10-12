package marketsnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// SnapshotStatus 枚举。
const (
	StatusBuilding = "building"
	StatusReady    = "ready"
	StatusFailed   = "failed"
	StatusPartial  = "partial"
)

// UniverseDefinition 统一定义：名称 + 过滤规则。
// 常用的 universe_csi800 / universe_all_a / universe_usable 都是它的实例。
type UniverseDefinition struct {
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	ExcludeST         bool     `json:"exclude_st"`
	ExcludeSuspended  bool     `json:"exclude_suspended"`
	ExcludeDelisted   bool     `json:"exclude_delisted"`
	MinIpoDays        int      `json:"min_ipo_days,omitempty"`
	RequiredCodes     []string `json:"required_codes,omitempty"`
	IndexComponentsOf string   `json:"index_components_of,omitempty"` // 例如 "000906.SH" = 中证800
	Board             string   `json:"board,omitempty"`               // main / gem / star / beijing / ''
}

// UniverseMember 是 universe 中的单条结果，带 point-in-time 状态。
type UniverseMember struct {
	Code           string   `json:"code"`
	Name           string   `json:"name,omitempty"`
	Market         string   `json:"market,omitempty"` // SH/SZ/BJ
	Board          string   `json:"board,omitempty"`
	Status         string   `json:"status"` // normal/st/suspended/delisted/halted
	IpoDate        string   `json:"ipo_date,omitempty"`
	DelistDate     string   `json:"delist_date,omitempty"`
	Selected       bool     `json:"selected"` // 是否被 UniverseDefinition 选中（未通过规则也会保留但=false）
	ExcludeReasons []string `json:"exclude_reasons,omitempty"`
}

// CodeStatus 单股在某快照日的数据水位，供 readiness 判定。
type CodeStatus struct {
	Code           string `json:"code"`
	UniverseMember bool   `json:"universe_member"`
	SecurityStatus string `json:"security_status"` // from universe: normal/st/suspended/delisted/halted
	IpoDate        string `json:"ipo_date,omitempty"`
	DelistDate     string `json:"delist_date,omitempty"`
	KlineLastDate  string `json:"kline_last_date,omitempty"`
	KlineRowCount  int    `json:"kline_row_count"`
	QuoteReady     bool   `json:"quote_ready"`
	FinanceReady   bool   `json:"finance_ready"`
	XdxrReady      bool   `json:"xdxr_ready"`
	GapDays        int    `json:"gap_days"`
	LastError      string `json:"last_error,omitempty"`
}

// MarketSnapshot 是每日业务上的不可变快照：一旦 Frozen=true，所有字段不再修改。
// 内容哈希包含 universe + 所有代码水位，因此即使"同个交易日"重跑也可以通过 hash
// 明确区分数据水位变化。
type MarketSnapshot struct {
	ID                 string             `json:"id"`
	SnapshotDate       string             `json:"snapshot_date"`
	Universe           UniverseDefinition `json:"universe"`
	Market             string             `json:"market"`
	PriceAdjustment    string             `json:"price_adjustment"`
	ExpectedKlineCodes int                `json:"expected_kline_codes"`
	ReadyKlineCodes    int                `json:"ready_kline_codes"`
	ReadyQuoteCodes    int                `json:"ready_quote_codes"`
	ReadyFinanceCodes  int                `json:"ready_finance_codes"`
	ReadyXdxrCodes     int                `json:"ready_xdxr_codes"`
	CoveragePct        float64            `json:"coverage_pct"`
	Status             string             `json:"status"`
	ReadinessReason    string             `json:"readiness_reason,omitempty"`
	UniverseHash       string             `json:"universe_hash"`
	ContentHash        string             `json:"content_hash"`
	Frozen             bool               `json:"frozen"`
	BuiltAt            time.Time          `json:"built_at,omitempty"`
	FrozenAt           time.Time          `json:"frozen_at,omitempty"`

	// 代码水位明细（可选；写入 DB 时拆成 market_snapshot_code_state 表）
	Codes []CodeStatus `json:"codes,omitempty"`
	// 已选的 universe 成员（Codes 中 UniverseMember=true & Selected=true 的子集）
	UniverseMembers []UniverseMember `json:"universe_members,omitempty"`
}

// Validate 基本字段校验。
func (s *MarketSnapshot) Validate() error {
	if s.SnapshotDate == "" {
		return fmt.Errorf("snapshot_date is required")
	}
	if s.Universe.Name == "" {
		return fmt.Errorf("universe.name is required")
	}
	if _, err := time.Parse("2006-01-02", s.SnapshotDate); err != nil {
		return fmt.Errorf("snapshot_date must be YYYY-MM-DD: %w", err)
	}
	return nil
}

// ComputeUniverseHash 对成员代码（按字母排序）做哈希，保证同名单稳定。
func ComputeUniverseHash(members []UniverseMember) string {
	codes := make([]string, 0, len(members))
	for _, m := range members {
		if m.Selected {
			codes = append(codes, m.Code)
		}
	}
	sort.Strings(codes)
	h := sha256.New()
	for _, c := range codes {
		h.Write([]byte(c))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeContentHash 汇总整个快照的内容（universe + codes 水位）。
func ComputeContentHash(s *MarketSnapshot) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil snapshot")
	}
	sortedCodes := append([]CodeStatus(nil), s.Codes...)
	sort.Slice(sortedCodes, func(i, j int) bool { return sortedCodes[i].Code < sortedCodes[j].Code })
	payload := struct {
		Date     string       `json:"date"`
		Universe string       `json:"universe"`
		Adj      string       `json:"adj"`
		UHash    string       `json:"uhash"`
		Codes    []CodeStatus `json:"codes"`
	}{
		Date:     s.SnapshotDate,
		Universe: s.Universe.Name,
		Adj:      s.PriceAdjustment,
		UHash:    s.UniverseHash,
		Codes:    sortedCodes,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// DefaultUniverseUsable 返回 A 股常用的可交易股票池。
func DefaultUniverseUsable() UniverseDefinition {
	return UniverseDefinition{
		Name:             "universe_usable",
		Description:      "除 ST、停牌、退市、上市不满 60 天的 A 股",
		ExcludeST:        true,
		ExcludeSuspended: true,
		ExcludeDelisted:  true,
		MinIpoDays:       60,
	}
}

// DefaultUniverseCSI800 返回 CSI800 成分股叠加可用规则。
func DefaultUniverseCSI800() UniverseDefinition {
	return UniverseDefinition{
		Name:              "universe_csi800",
		Description:       "中证 800 成分股，叠加可交易过滤",
		ExcludeST:         true,
		ExcludeSuspended:  true,
		ExcludeDelisted:   true,
		MinIpoDays:        60,
		IndexComponentsOf: "000906.SH",
	}
}

// DefaultUniverseAllA 不过滤（仅排已退市），用于数据水位审计。
func DefaultUniverseAllA() UniverseDefinition {
	return UniverseDefinition{
		Name:            "universe_all_a",
		Description:     "所有在 stockinfo 中的 A 股，仅排已退市",
		ExcludeDelisted: true,
	}
}

// FeatureSnapshot 对应一张 feature_snapshot：对固定 feature 列表做全市场物化。
type FeatureSnapshot struct {
	ID               string    `json:"id"`
	MarketSnapshotID string    `json:"market_snapshot_id"`
	SnapshotDate     string    `json:"snapshot_date"`
	FeatureIDs       []string  `json:"feature_ids"`
	FeatureTotal     int       `json:"feature_total"`
	RowsWritten      int       `json:"rows_written"`
	LeakChecked      bool      `json:"leak_checked"`
	PriceAdjustment  string    `json:"price_adjustment"`
	Status           string    `json:"status"`
	AsOfNs           int64     `json:"as_of_ns"`
	ContentHash      string    `json:"content_hash"`
	BuiltAt          time.Time `json:"built_at,omitempty"`
	// code -> feature_id -> value
	Values map[string]map[string]float64 `json:"values,omitempty"`
}

// ComputeFeatureContentHash 对 Values 排序后哈希，保证确定性。
func ComputeFeatureContentHash(s *FeatureSnapshot) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil feature snapshot")
	}
	codes := make([]string, 0, len(s.Values))
	for c := range s.Values {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	features := append([]string(nil), s.FeatureIDs...)
	sort.Strings(features)
	h := sha256.New()
	h.Write([]byte(s.SnapshotDate))
	h.Write([]byte{0})
	h.Write([]byte(s.PriceAdjustment))
	h.Write([]byte{0})
	for _, c := range codes {
		h.Write([]byte(c))
		h.Write([]byte{0})
		for _, f := range features {
			v, ok := s.Values[c][f]
			if !ok {
				h.Write([]byte{0xff})
				continue
			}
			// 用格式化避免 float64 NaN/Inf 不一致
			fmt.Fprintf(h, "%q=%.8g|", f, v)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FeatureSpec 对应 methods.DSL 里 "indicator" 名称 → 特征规格映射。
// 不引入 paradigm 特征仓库，保证 marketsnapshot 只依赖 methods 域定义。
type FeatureSpec struct {
	Name        string `json:"name"`
	Window      int    `json:"window"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
	// DefaultFeatures() 返回 methods 用的核心列表，其他扩展在 adapter 注册。
}

// DefaultDslFeatures 返回 methods.DSL 内建指标清单，供特征物化默认覆盖。
func DefaultDslFeatures() []FeatureSpec {
	return []FeatureSpec{
		{Name: "close", Category: "price", Window: 1, Description: "收盘价格"},
		{Name: "open", Category: "price", Window: 1},
		{Name: "high", Category: "price", Window: 1},
		{Name: "low", Category: "price", Window: 1},
		{Name: "volume", Category: "price", Window: 1},
		{Name: "amount", Category: "price", Window: 1},
		// 单日执行引擎只持有冻结特征快照、无法回看历史 bar，所以像
		// gap_pct/return1 这类需要前收盘的内建指标必须在这里物化；
		// 否则引用它们的入场规则会静默地永不命中（missingFeatures 的
		// fail-closed 守卫也覆盖不到 builtin 指标）。
		{Name: "gap_pct", Category: "price", Window: 2, Description: "当日开盘相对前收盘的跳空幅度"},
		{Name: "return1", Category: "price", Window: 2, Description: "当日收盘相对前收盘的单日收益"},
		{Name: "ma5", Category: "ma", Window: 5},
		{Name: "ma10", Category: "ma", Window: 10},
		{Name: "ma20", Category: "ma", Window: 20},
		{Name: "ma60", Category: "ma", Window: 60},
		{Name: "ma120", Category: "ma", Window: 120},
		{Name: "ma250", Category: "ma", Window: 250},
		{Name: "rsi6", Category: "rsi", Window: 6},
		{Name: "rsi14", Category: "rsi", Window: 14},
		{Name: "rsi24", Category: "rsi", Window: 24},
		// 动能与量能特征：内置示例方法（MACD 多头 / 量价突破）与执行器共用
		// methods.ComputeBuiltinIndicator 的唯一实现，保证研究-交易一致。
		{Name: "macd_dif", Category: "macd", Window: 26, Description: "MACD 快慢线之差"},
		{Name: "macd_dea", Category: "macd", Window: 35, Description: "MACD 信号线"},
		{Name: "macd_hist", Category: "macd", Window: 35, Description: "MACD 柱状"},
		{Name: "volma5", Category: "volume", Window: 5, Description: "5 日均量"},
		{Name: "volma20", Category: "volume", Window: 20, Description: "20 日均量"},
		{Name: "volratio5", Category: "volume", Window: 6, Description: "量比：当日成交量/前5日均量"},
		{Name: "volratio20", Category: "volume", Window: 21, Description: "量比：当日成交量/前20日均量"},
		{Name: "obv20", Category: "volume", Window: 21, Description: "20 日能量潮净额"},
		{Name: "prevhigh20", Category: "breakout", Window: 20, Description: "前 20 日收盘最高（不含当日）"},
		{Name: "prevlow20", Category: "breakout", Window: 20, Description: "前 20 日收盘最低（不含当日）"},
		{Name: "prevhigh60", Category: "breakout", Window: 60, Description: "前 60 日收盘最高"},
		{Name: "prevlow60", Category: "breakout", Window: 60, Description: "前 60 日收盘最低"},
		// 动量/反转族：ret{n} 与 return1 同源，窗口化便于挖掘不持有期的动量。
		{Name: "ret5", Category: "momentum", Window: 6, Description: "5 日收益"},
		{Name: "ret10", Category: "momentum", Window: 11, Description: "10 日收益"},
		{Name: "ret20", Category: "momentum", Window: 21, Description: "20 日收益"},
		{Name: "ret60", Category: "momentum", Window: 61, Description: "60 日收益"},
		{Name: "ret120", Category: "momentum", Window: 121, Description: "120 日收益"},
		{Name: "ema12", Category: "ma", Window: 12, Description: "12 日指数均线"},
		{Name: "ema26", Category: "ma", Window: 26, Description: "26 日指数均线"},
		{Name: "bias5", Category: "ma", Window: 5, Description: "5 日乖离率"},
		{Name: "bias10", Category: "ma", Window: 10, Description: "10 日乖离率"},
		{Name: "bias20", Category: "ma", Window: 20, Description: "20 日乖离率"},
		// 震荡/超买超卖族：KDJ/CCI/威廉/RSI 提供不同平滑口径的摆动特征。
		{Name: "kdj_k", Category: "oscillator", Window: 9, Description: "KDJ 的 K 值"},
		{Name: "kdj_d", Category: "oscillator", Window: 9, Description: "KDJ 的 D 值"},
		{Name: "kdj_j", Category: "oscillator", Window: 9, Description: "KDJ 的 J 值"},
		{Name: "boll_upper", Category: "volatility", Window: 20, Description: "布林上轨"},
		{Name: "boll_mid", Category: "volatility", Window: 20, Description: "布林中轨"},
		{Name: "boll_lower", Category: "volatility", Window: 20, Description: "布林下轨"},
		{Name: "cci14", Category: "oscillator", Window: 14, Description: "CCI(14)"},
		{Name: "willr20", Category: "oscillator", Window: 20, Description: "威廉 %R(20)"},
		// 波动/风险族：ATR 与波动率给出绝对与相对两种波动度量。
		{Name: "atr14", Category: "volatility", Window: 14, Description: "14 日平均真实波幅"},
		{Name: "atrp14", Category: "volatility", Window: 14, Description: "14 日 ATR 占收盘价比例"},
		{Name: "volatility5", Category: "volatility", Window: 6, Description: "5 日收益波动率"},
		{Name: "volatility20", Category: "volatility", Window: 21, Description: "20 日收益波动率"},
		{Name: "volatility60", Category: "volatility", Window: 61, Description: "60 日收益波动率"},
		{Name: "amihud20", Category: "liquidity", Window: 21, Description: "20 日 Amihud 非流动性因子（×1e9）"},
		// 结构/日内强度族：收盘位置、影线、连涨、上涨天数占比。
		{Name: "amplitude1", Category: "structure", Window: 2, Description: "当日振幅 (H-L)/前收"},
		{Name: "closepos", Category: "structure", Window: 1, Description: "收盘在当日高低区间的位置"},
		{Name: "upshadow", Category: "structure", Window: 2, Description: "上影线占前收比例"},
		{Name: "downshadow", Category: "structure", Window: 2, Description: "下影线占前收比例"},
		{Name: "upstreak", Category: "structure", Window: 2, Description: "连涨天数"},
		{Name: "updays20", Category: "structure", Window: 21, Description: "20 日上涨天数占比"},
		{Name: "updays60", Category: "structure", Window: 61, Description: "60 日上涨天数占比"},
		// 距离极值族：相对前 N 日高/低点的百分比距离，度量超买超卖与突破空间。
		{Name: "offhigh60", Category: "breakout", Window: 61, Description: "距前 60 日高点百分比"},
		{Name: "offhigh250", Category: "breakout", Window: 251, Description: "距前 250 日高点百分比"},
		{Name: "offlow60", Category: "breakout", Window: 61, Description: "距前 60 日低点百分比"},
		{Name: "offlow250", Category: "breakout", Window: 251, Description: "距前 250 日低点百分比"},
	}
}

// LatestSelectionFeatures extends the historical-safe DSL set with the
// current market-cap input used by a latest-day scope. It must not be used for
// historical validation: stockinfo has no point-in-time market-cap series.
func LatestSelectionFeatures() []FeatureSpec {
	features := DefaultDslFeatures()
	return append([]FeatureSpec{
		{Name: "market_cap", Category: "scope", Window: 1, Description: "当前流通市值（亿元），仅用于最新快照筛选"},
		{Name: "total_market_cap", Category: "scope", Window: 1, Description: "当前总市值（亿元），仅用于最新快照筛选"},
		{Name: "turnover_rate", Category: "scope", Window: 1, Description: "当前换手率（%），仅用于最新快照筛选"},
		{Name: "float_shares", Category: "scope", Window: 1, Description: "当前流通股本（万股），仅用于最新快照筛选"},
		{Name: "avg_shares_per_holder", Category: "scope", Window: 1, Description: "当前户均持股（流通股本/股东户数，股/户），仅用于最新快照筛选"},
	}, features...)
}

// FeatureNames 返回特征规格的名字集合（去重、升序），用于快照覆盖检查。
func FeatureNames(specs []FeatureSpec) []string {
	seen := map[string]bool{}
	for _, s := range specs {
		if s.Name != "" {
			seen[s.Name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// MissingFeatures 返回 required 中未被本快照物化的名字（去重、升序）。
// 这是「发现端与选股端特征一致性」的判定点：选股/复用快照前用它过滤升级
// 前物化的旧快照（如 gap_pct 进入 DefaultDslFeatures 之前的 22 特征集），
// 避免方法因缺特征整批 insufficient_data。
func (s *FeatureSnapshot) MissingFeatures(required []string) []string {
	have := map[string]bool{}
	if s != nil {
		for _, f := range s.FeatureIDs {
			have[f] = true
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, r := range required {
		if !have[r] && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}
