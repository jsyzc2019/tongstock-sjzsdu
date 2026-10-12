package methods

import (
	"math"
	"strings"
)

// ComputeBuiltinIndicator 在升序 bars 上计算内建指标，最后一根视为执行日。
//
// 这是执行器（在线判定）与离线特征物化（选股链路）共用的唯一实现，
// 保证「研究时算出的值」与「交易时算出的值」完全一致。
// 返回 false 表示数据不足或指标名不受支持——调用方必须 fail closed，
// 禁止用零值或默认值冒充真实计算。
func ComputeBuiltinIndicator(name string, bars []Bar) (float64, bool) {
	if len(bars) == 0 {
		return 0, false
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	last := bars[len(bars)-1]
	switch lower {
	case "open":
		return last.Open, true
	case "high":
		return last.High, true
	case "low":
		return last.Low, true
	case "close":
		return last.Close, true
	case "volume":
		return last.Volume, true
	case "amount":
		return last.Amount, true
	}
	switch {
	case strings.HasPrefix(lower, "volma"):
		if n, err := parseIntSuffix(lower, len("volma")); err == nil && n > 0 {
			return smaVolume(bars, n)
		}
	case strings.HasPrefix(lower, "volratio"):
		if n, err := parseIntSuffix(lower, len("volratio")); err == nil && n > 0 {
			return volumeRatio(bars, n)
		}
	case strings.HasPrefix(lower, "volatility"):
		if n, err := parseIntSuffix(lower, len("volatility")); err == nil && n > 1 {
			return rollingVolatility(bars, n)
		}
	case strings.HasPrefix(lower, "obv"):
		if n, err := parseIntSuffix(lower, len("obv")); err == nil && n > 0 {
			return onBalanceVolume(bars, n)
		}
	case strings.HasPrefix(lower, "amihud"):
		if n, err := parseIntSuffix(lower, len("amihud")); err == nil && n > 0 {
			return amihudIlliquidity(bars, n)
		}
	case strings.HasPrefix(lower, "amplitude"):
		if n, err := parseIntSuffix(lower, len("amplitude")); err == nil && n > 0 {
			return amplitude(bars, n)
		}
	case strings.HasPrefix(lower, "offhigh"):
		if n, err := parseIntSuffix(lower, len("offhigh")); err == nil && n > 0 {
			return offExtreme(bars, n, true)
		}
	case strings.HasPrefix(lower, "offlow"):
		if n, err := parseIntSuffix(lower, len("offlow")); err == nil && n > 0 {
			return offExtreme(bars, n, false)
		}
	case strings.HasPrefix(lower, "updays"):
		if n, err := parseIntSuffix(lower, len("updays")); err == nil && n > 0 {
			return upDaysRatio(bars, n)
		}
	case strings.HasPrefix(lower, "bias"):
		if n, err := parseIntSuffix(lower, len("bias")); err == nil && n > 0 {
			return biasValue(bars, n)
		}
	case strings.HasPrefix(lower, "cci"):
		if n, err := parseIntSuffix(lower, len("cci")); err == nil && n > 0 {
			return cciValue(bars, n)
		}
	case strings.HasPrefix(lower, "willr"):
		if n, err := parseIntSuffix(lower, len("willr")); err == nil && n > 0 {
			return williamsR(bars, n)
		}
	case strings.HasPrefix(lower, "ret"):
		// ret{n} = close/close[-(n+1)]-1；n=1 即 return1 的语义。
		if n, err := parseIntSuffix(lower, len("ret")); err == nil && n > 0 {
			return returnOverBars(bars, n)
		}
	case strings.HasPrefix(lower, "atrp"):
		// atrp 必须在 atr 之前：否则 atrp14 会被 atr 前缀吞掉（Atoi("p14") 失败即丢弃）。
		if n, err := parseIntSuffix(lower, len("atrp")); err == nil && n > 0 {
			return atrPercent(bars, n)
		}
	case strings.HasPrefix(lower, "atr"):
		if n, err := parseIntSuffix(lower, len("atr")); err == nil && n > 0 {
			return averageTrueRange(bars, n)
		}
	case strings.HasPrefix(lower, "prevhigh"):
		if n, err := parseIntSuffix(lower, len("prevhigh")); err == nil && n > 0 {
			return previousCloseExtreme(bars, n, true)
		}
	case strings.HasPrefix(lower, "prevlow"):
		if n, err := parseIntSuffix(lower, len("prevlow")); err == nil && n > 0 {
			return previousCloseExtreme(bars, n, false)
		}
	case strings.HasPrefix(lower, "rsi"):
		if n, err := parseIntSuffix(lower, len("rsi")); err == nil && n > 0 {
			return rsiValue(bars, n)
		}
	case strings.HasPrefix(lower, "ema"):
		if n, err := parseIntSuffix(lower, len("ema")); err == nil && n > 0 {
			return emaValue(bars, n)
		}
	case strings.HasPrefix(lower, "ma"):
		if n, err := parseIntSuffix(lower, len("ma")); err == nil && n > 0 {
			return smaClose(bars, n)
		}
	}
	switch lower {
	case "macd_dif", "dif":
		dif, _, ok := macdValues(bars)
		return dif, ok
	case "macd_dea", "dea":
		_, dea, ok := macdValues(bars)
		return dea, ok
	case "macd_hist", "macd":
		dif, dea, ok := macdValues(bars)
		if !ok {
			return 0, false
		}
		return 2 * (dif - dea), true
	case "kdj_k", "kdj_d", "kdj_j":
		k, d, ok := kdjValues(bars)
		if !ok {
			return 0, false
		}
		switch lower {
		case "kdj_k":
			return k, true
		case "kdj_d":
			return d, true
		default:
			return 3*k - 2*d, true
		}
	case "boll_mid", "boll_upper", "boll_lower":
		upper, mid, lowerBand, ok := bollBands3(bars)
		if !ok {
			return 0, false
		}
		switch lower {
		case "boll_upper":
			return upper, true
		case "boll_lower":
			return lowerBand, true
		default:
			return mid, true
		}
	case "closepos":
		return closePosition(bars)
	case "upshadow":
		return shadowRatio(bars, true)
	case "downshadow":
		return shadowRatio(bars, false)
	case "upstreak":
		return upStreak(bars)
	case "return1":
		if len(bars) >= 2 {
			previous := bars[len(bars)-2].Close
			if previous > 0 {
				return (last.Close - previous) / previous, true
			}
		}
	case "gap_pct":
		if len(bars) >= 2 {
			previous := bars[len(bars)-2].Close
			if previous > 0 {
				return (last.Open - previous) / previous, true
			}
		}
	}
	return 0, false
}

// MACD 标准参数 (12, 26, 9)。
const (
	macdFast   = 12
	macdSlow   = 26
	macdSignal = 9
)

// macdValues 返回 (DIF, DEA)。需要至少 macdSlow + macdSignal 根 K 线才能稳定。
func macdValues(bars []Bar) (float64, float64, bool) {
	if len(bars) < macdSlow+macdSignal {
		return 0, 0, false
	}
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}
	fast := emaSeries(closes, macdFast)
	slow := emaSeries(closes, macdSlow)
	difSeries := make([]float64, len(closes))
	for i := range closes {
		difSeries[i] = fast[i] - slow[i]
	}
	deaSeries := emaSeries(difSeries, macdSignal)
	return difSeries[len(closes)-1], deaSeries[len(closes)-1], true
}

// emaSeries 返回与输入等长的 EMA 序列（前值用简单均值播种）。
func emaSeries(values []float64, n int) []float64 {
	out := make([]float64, len(values))
	if len(values) == 0 || n <= 0 {
		return out
	}
	k := 2.0 / float64(n+1)
	seed := values[0]
	out[0] = seed
	for i := 1; i < len(values); i++ {
		out[i] = values[i]*k + out[i-1]*(1-k)
	}
	return out
}

func emaValue(bars []Bar, n int) (float64, bool) {
	if len(bars) < n {
		return 0, false
	}
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}
	series := emaSeries(closes, n)
	return series[len(series)-1], true
}

func smaClose(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n {
		return 0, false
	}
	sum := 0.0
	for i := len(bars) - n; i < len(bars); i++ {
		sum += bars[i].Close
	}
	return sum / float64(n), true
}

func smaVolume(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n {
		return 0, false
	}
	var sum float64
	for i := len(bars) - n; i < len(bars); i++ {
		sum += bars[i].Volume
	}
	return sum / float64(n), true
}

// previousCloseExtreme 返回当前 bar 之前 n 根的收盘极值（不含当日，避免前视）。
func previousCloseExtreme(bars []Bar, n int, maximum bool) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	start, end := len(bars)-n-1, len(bars)-1
	value := bars[start].Close
	for i := start + 1; i < end; i++ {
		if maximum && bars[i].Close > value {
			value = bars[i].Close
		}
		if !maximum && bars[i].Close < value {
			value = bars[i].Close
		}
	}
	return value, true
}

func rollingVolatility(bars []Bar, n int) (float64, bool) {
	if n <= 1 || len(bars) < n+1 {
		return 0, false
	}
	returns := make([]float64, 0, n)
	for i := len(bars) - n; i < len(bars); i++ {
		previous := bars[i-1].Close
		if previous <= 0 {
			return 0, false
		}
		returns = append(returns, (bars[i].Close-previous)/previous)
	}
	var mean float64
	for _, value := range returns {
		mean += value
	}
	mean /= float64(len(returns))
	var variance float64
	for _, value := range returns {
		delta := value - mean
		variance += delta * delta
	}
	return math.Sqrt(variance / float64(len(returns)-1)), true
}

func rsiValue(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	start := len(bars) - n - 1
	var gains, losses float64
	count := 0
	for i := start + 1; i < len(bars); i++ {
		diff := bars[i].Close - bars[i-1].Close
		if diff > 0 {
			gains += diff
		} else {
			losses -= diff
		}
		count++
	}
	if count == 0 || losses == 0 {
		if gains == 0 {
			return 50.0, true
		}
		return 100.0, true
	}
	avgGain := gains / float64(count)
	avgLoss := losses / float64(count)
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs), true
}

// returnOverBars 返回 close/close[-(n+1)]-1。n=1 时与 return1 语义一致。
func returnOverBars(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	base := bars[len(bars)-1-n].Close
	if base <= 0 {
		return 0, false
	}
	return bars[len(bars)-1].Close/base - 1, true
}

// biasValue 乖离率 = (close - MA_n) / MA_n。
func biasValue(bars []Bar, n int) (float64, bool) {
	ma, ok := smaClose(bars, n)
	if !ok || ma <= 0 {
		return 0, false
	}
	return (bars[len(bars)-1].Close - ma) / ma, true
}

// kdjValues 计算 KDJ(9,3,3)。RSV 用最近 9 根（含当日）的 (close-low9)/(high9-low9)*100；
// K/D 用 SMA 平滑，种子 K=D=50。需要至少 9 根。
func kdjValues(bars []Bar) (float64, float64, bool) {
	const n = 9
	if len(bars) < n {
		return 0, 0, false
	}
	k, d := 50.0, 50.0
	for i := n - 1; i < len(bars); i++ {
		window := bars[i-n+1 : i+1]
		hi, lo := window[0].High, window[0].Low
		for _, b := range window[1:] {
			if b.High > hi {
				hi = b.High
			}
			if b.Low < lo {
				lo = b.Low
			}
		}
		rng := hi - lo
		var rsv float64
		if rng > 0 {
			rsv = (bars[i].Close - lo) / rng * 100
		}
		k = 2.0/3*k + 1.0/3*rsv
		d = 2.0/3*d + 1.0/3*k
	}
	return k, d, true
}

// bollBands3 计算 BOLL(20,2)。返回 (upper, mid, lower)。mid = MA20(close)，
// sd 用总体标准差（除以 n，非 n-1，与通达信口径一致）。需要至少 20 根。
func bollBands3(bars []Bar) (float64, float64, float64, bool) {
	const n = 20
	if len(bars) < n {
		return 0, 0, 0, false
	}
	window := bars[len(bars)-n:]
	mean := 0.0
	for _, b := range window {
		mean += b.Close
	}
	mean /= float64(n)
	variance := 0.0
	for _, b := range window {
		delta := b.Close - mean
		variance += delta * delta
	}
	sd := math.Sqrt(variance / float64(n))
	return mean + 2*sd, mean, mean - 2*sd, true
}

// cciValue CCI(n)：(TP - MA(TP,n)) / (0.015 * MD)，TP=(H+L+C)/3，MD 为平均绝对偏差。
func cciValue(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n {
		return 0, false
	}
	window := bars[len(bars)-n:]
	tp := make([]float64, n)
	mean := 0.0
	for i, b := range window {
		tp[i] = (b.High + b.Low + b.Close) / 3
		mean += tp[i]
	}
	mean /= float64(n)
	md := 0.0
	for _, v := range tp {
		md += math.Abs(v - mean)
	}
	md /= float64(n)
	if md == 0 {
		return 0, true
	}
	return (tp[n-1] - mean) / (0.015 * md), true
}

// williamsR 威廉指标 %R(n) = (HH_n - close) / (HH_n - LL_n) * -100。
// HH/LL 取最近 n 根（含当日）的最高/最低。需要至少 n 根且区间非零。
func williamsR(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n {
		return 0, false
	}
	window := bars[len(bars)-n:]
	hi, lo := window[0].High, window[0].Low
	for _, b := range window[1:] {
		if b.High > hi {
			hi = b.High
		}
		if b.Low < lo {
			lo = b.Low
		}
	}
	rng := hi - lo
	if rng <= 0 {
		return 0, false
	}
	return (hi - bars[len(bars)-1].Close) / rng * -100, true
}

// trueRange 单根真实波幅：max(H-L, |H-prevClose|, |L-prevClose|)。
// 首根（无前收）退化为 H-L。
func trueRange(bars []Bar, i int) float64 {
	b := bars[i]
	hl := b.High - b.Low
	if i == 0 {
		return hl
	}
	prev := bars[i-1].Close
	return math.Max(hl, math.Max(math.Abs(b.High-prev), math.Abs(b.Low-prev)))
}

// averageTrueRange ATR(n)：最近 n 根 trueRange 的简单均值。
func averageTrueRange(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n {
		return 0, false
	}
	sum := 0.0
	for i := len(bars) - n; i < len(bars); i++ {
		sum += trueRange(bars, i)
	}
	return sum / float64(n), true
}

// atrPercent ATR 占收盘价比例 = ATR_n / close。
func atrPercent(bars []Bar, n int) (float64, bool) {
	atr, ok := averageTrueRange(bars, n)
	if !ok {
		return 0, false
	}
	close := bars[len(bars)-1].Close
	if close <= 0 {
		return 0, false
	}
	return atr / close, true
}

// volumeRatio 量比 = 当日成交量 / 前 n 日均量。
func volumeRatio(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	var sum float64
	for i := len(bars) - n - 1; i < len(bars)-1; i++ {
		sum += bars[i].Volume
	}
	avg := sum / float64(n)
	if avg <= 0 {
		return 0, false
	}
	return bars[len(bars)-1].Volume / avg, true
}

// onBalanceVolume OBV(n)：最近 n 根按涨跌累加的成交量净额。
func onBalanceVolume(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	obv := 0.0
	for i := len(bars) - n; i < len(bars); i++ {
		switch {
		case bars[i].Close > bars[i-1].Close:
			obv += bars[i].Volume
		case bars[i].Close < bars[i-1].Close:
			obv -= bars[i].Volume
		}
	}
	return obv, true
}

// amihudIlliquidity 非流动性因子 mean(|ret|/amount)，按 1e9 缩放便于阈值设定。
// 使用最近 n 根（每日 |close/prevClose-1|/amount 的均值）。
func amihudIlliquidity(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	var sum float64
	count := 0
	for i := len(bars) - n; i < len(bars); i++ {
		prev := bars[i-1].Close
		if prev <= 0 || bars[i].Amount <= 0 {
			continue
		}
		sum += math.Abs(bars[i].Close/prev-1) / bars[i].Amount
		count++
	}
	if count == 0 {
		return 0, false
	}
	return sum / float64(count) * 1e9, true
}

// amplitude 振幅 = mean((H-L)/prevClose) over last n bars。
func amplitude(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	var sum float64
	for i := len(bars) - n; i < len(bars); i++ {
		prev := bars[i-1].Close
		if prev <= 0 {
			return 0, false
		}
		sum += (bars[i].High - bars[i].Low) / prev
	}
	return sum / float64(n), true
}

// closePosition 收盘位置 = (close-low)/(high-low)。H==L 时 fail closed。
func closePosition(bars []Bar) (float64, bool) {
	last := bars[len(bars)-1]
	rng := last.High - last.Low
	if rng <= 0 {
		return 0, false
	}
	return (last.Close - last.Low) / rng, true
}

// shadowRatio 上/下影线占前收比例。up=true 返回上影 (H-max(O,C))/prevClose，
// 否则下影 (min(O,C)-L)/prevClose。需要前一根收盘。
func shadowRatio(bars []Bar, up bool) (float64, bool) {
	if len(bars) < 2 {
		return 0, false
	}
	last := bars[len(bars)-1]
	prev := bars[len(bars)-2].Close
	if prev <= 0 {
		return 0, false
	}
	var body, shadow float64
	if up {
		body = math.Max(last.Open, last.Close)
		shadow = last.High - body
	} else {
		body = math.Min(last.Open, last.Close)
		shadow = body - last.Low
	}
	if shadow < 0 {
		shadow = 0
	}
	return shadow / prev, true
}

// offExtreme 距离前 n 日极值的百分比 = close/prevExtreme - 1。
// 复用 previousCloseExtreme（不含当日，避免前视）。
func offExtreme(bars []Bar, n int, fromHigh bool) (float64, bool) {
	extreme, ok := previousCloseExtreme(bars, n, fromHigh)
	if !ok || extreme <= 0 {
		return 0, false
	}
	return bars[len(bars)-1].Close/extreme - 1, true
}

// upStreak 连涨天数：从最后一根往前数连续 close 上涨的根数。
func upStreak(bars []Bar) (float64, bool) {
	if len(bars) < 2 {
		return 0, false
	}
	streak := 0
	for i := len(bars) - 1; i > 0; i-- {
		if bars[i].Close > bars[i-1].Close {
			streak++
		} else {
			break
		}
	}
	return float64(streak), true
}

// upDaysRatio 最近 n 根中上涨的占比 = 上涨根数 / n。
func upDaysRatio(bars []Bar, n int) (float64, bool) {
	if n <= 0 || len(bars) < n+1 {
		return 0, false
	}
	up := 0
	for i := len(bars) - n; i < len(bars); i++ {
		if bars[i].Close > bars[i-1].Close {
			up++
		}
	}
	return float64(up) / float64(n), true
}
