package methods

import (
	"math"
	"testing"
)

// risingBars 构造严格递增、收盘=最高的合成序列：close_i = 10+i, high = close,
// low = close-1, open = close-0.5, volume = 1000*(i+1)。
func risingBars(n int) []Bar {
	bars := make([]Bar, 0, n)
	for i := 0; i < n; i++ {
		c := 10 + float64(i)
		bars = append(bars, Bar{
			Date:   "2024-01-02",
			Open:   c - 0.5,
			High:   c,
			Low:    c - 1,
			Close:  c,
			Volume: 1000 * float64(i+1),
			Amount: 1000 * float64(i+1) * c,
		})
	}
	return bars
}

func almost(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6*math.Max(1, math.Abs(want)) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func mustCompute(t *testing.T, name string, bars []Bar) float64 {
	t.Helper()
	v, ok := ComputeBuiltinIndicator(name, bars)
	if !ok {
		t.Fatalf("%s should be computable on %d bars", name, len(bars))
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		t.Fatalf("%s = %v (NaN/Inf)", name, v)
	}
	return v
}

func TestRetFamily(t *testing.T) {
	bars := risingBars(30) // close_29 = 39
	// ret5 = close/close[-6]-1 = 39/34-1
	almost(t, "ret5", mustCompute(t, "ret5", bars), 39.0/34.0-1)
	almost(t, "ret10", mustCompute(t, "ret10", bars), 39.0/29.0-1)
	almost(t, "ret20", mustCompute(t, "ret20", bars), 39.0/19.0-1)
	// return1 兼容别名
	almost(t, "return1", mustCompute(t, "return1", bars), 39.0/38.0-1)
	if _, ok := ComputeBuiltinIndicator("ret5", bars[:5]); ok {
		t.Fatal("ret5 on 5 bars must fail closed (needs 6)")
	}
}

func TestBiasFamily(t *testing.T) {
	bars := risingBars(30)
	ma5 := (39.0 + 38.0 + 37.0 + 36.0 + 35.0) / 5
	almost(t, "bias5", mustCompute(t, "bias5", bars), (39.0-ma5)/ma5)
	if _, ok := ComputeBuiltinIndicator("bias5", bars[:4]); ok {
		t.Fatal("bias5 on 4 bars must fail closed")
	}
}

func TestKDJExactFirstBar(t *testing.T) {
	// 收盘=最高 → RSV 恒 100。种子 K0=D0=50。
	bars := risingBars(12)
	k := mustCompute(t, "kdj_k", bars)
	d := mustCompute(t, "kdj_d", bars)
	j := mustCompute(t, "kdj_j", bars)
	k1 := 2.0/3*50 + 1.0/3*100
	d1 := 2.0/3*50 + 1.0/3*k1
	// 12 根 → index 8..11 共 4 次迭代
	kk, dd := k1, d1
	for i := 0; i < 3; i++ {
		kk = 2.0/3*kk + 1.0/3*100
		dd = 2.0/3*dd + 1.0/3*kk
	}
	almost(t, "kdj_k", k, kk)
	almost(t, "kdj_d", d, dd)
	almost(t, "kdj_j", j, 3*kk-2*dd)
	if _, ok := ComputeBuiltinIndicator("kdj_k", bars[:8]); ok {
		t.Fatal("kdj_k on 8 bars must fail closed")
	}
}

func TestBOLLBands(t *testing.T) {
	flat := make([]Bar, 0, 20)
	for i := 0; i < 20; i++ {
		flat = append(flat, Bar{Open: 10, High: 10.5, Low: 9.5, Close: 10, Volume: 1e6, Amount: 1e7})
	}
	almost(t, "boll_mid(flat)", mustCompute(t, "boll_mid", flat), 10)
	almost(t, "boll_upper(flat)", mustCompute(t, "boll_upper", flat), 10)
	almost(t, "boll_lower(flat)", mustCompute(t, "boll_lower", flat), 10)

	bars := risingBars(20) // closes 10..29
	mean := 19.5
	std := math.Sqrt(399.0 / 12.0) // 总体标准差 sqrt((n^2-1)/12)
	almost(t, "boll_mid", mustCompute(t, "boll_mid", bars), mean)
	almost(t, "boll_upper", mustCompute(t, "boll_upper", bars), mean+2*std)
	almost(t, "boll_lower", mustCompute(t, "boll_lower", bars), mean-2*std)
	if _, ok := ComputeBuiltinIndicator("boll_upper", bars[:19]); ok {
		t.Fatal("boll_upper on 19 bars must fail closed")
	}
}

func TestCCI(t *testing.T) {
	flat := make([]Bar, 0, 20)
	for i := 0; i < 20; i++ {
		flat = append(flat, Bar{Open: 10, High: 10.5, Low: 9.5, Close: 10, Volume: 1e6, Amount: 1e7})
	}
	almost(t, "cci20(flat)", mustCompute(t, "cci20", flat), 0)

	// 独立公式复算（不调用实现）：tp=(H+L+C)/3, md=mean|tp-sma|
	bars := risingBars(20)
	tp := make([]float64, len(bars))
	for i, b := range bars {
		tp[i] = (b.High + b.Low + b.Close) / 3
	}
	sma := 0.0
	for _, v := range tp {
		sma += v
	}
	sma /= float64(len(tp))
	md := 0.0
	for _, v := range tp {
		md += math.Abs(v - sma)
	}
	md /= float64(len(tp))
	want := (tp[len(tp)-1] - sma) / (0.015 * md)
	almost(t, "cci20", mustCompute(t, "cci20", bars), want)
}

func TestWilliamsR(t *testing.T) {
	bars := risingBars(20)
	almost(t, "willr20(rising)", mustCompute(t, "willr20", bars), 0)
	falling := make([]Bar, 0, 20)
	for i := 0; i < 20; i++ {
		c := 30 - float64(i)
		falling = append(falling, Bar{Open: c, High: c, Low: c - 1, Close: c - 1, Volume: 1e6, Amount: 1e7})
	}
	// 收盘=区间最低 → -100
	almost(t, "willr20(falling)", mustCompute(t, "willr20", falling), -100)
}

func TestATRFamily(t *testing.T) {
	// H=10.5 L=9.5 C=10 恒定 → TR=max(1,0.5,0.5)=1
	bars := make([]Bar, 0, 15)
	for i := 0; i < 15; i++ {
		bars = append(bars, Bar{Open: 10, High: 10.5, Low: 9.5, Close: 10, Volume: 1e6, Amount: 1e7})
	}
	almost(t, "atr14", mustCompute(t, "atr14", bars), 1)
	almost(t, "atrp14", mustCompute(t, "atrp14", bars), 0.1)
	if _, ok := ComputeBuiltinIndicator("atr14", bars[:13]); ok {
		t.Fatal("atr14 on 13 bars must fail closed")
	}
}

func TestVolRatio(t *testing.T) {
	// 前 20 日均量 1000，当日 3000 → 量比 3
	bars := make([]Bar, 0, 21)
	for i := 0; i < 20; i++ {
		bars = append(bars, Bar{Open: 10, High: 10.5, Low: 9.5, Close: 10, Volume: 1000, Amount: 1e4})
	}
	bars = append(bars, Bar{Open: 10, High: 10.5, Low: 9.5, Close: 10, Volume: 3000, Amount: 3e4})
	almost(t, "volratio20", mustCompute(t, "volratio20", bars), 3)
	almost(t, "volratio5", mustCompute(t, "volratio5", bars), 3)
	if _, ok := ComputeBuiltinIndicator("volratio5", bars[:5]); ok {
		t.Fatal("volratio5 on 5 bars must fail closed (needs 6)")
	}
}

func TestOBVFamily(t *testing.T) {
	bars := risingBars(21)
	sum := 0.0
	for _, b := range bars[1:] {
		sum += b.Volume
	}
	almost(t, "obv20(up)", mustCompute(t, "obv20", bars), sum)
	falling := make([]Bar, 0, 21)
	for i := 0; i < 21; i++ {
		c := 30 - float64(i)
		falling = append(falling, Bar{Open: c, High: c, Low: c - 1, Close: c - 1, Volume: 1000, Amount: 1e4})
	}
	almost(t, "obv20(down)", mustCompute(t, "obv20", falling), -20*1000)
	if _, ok := ComputeBuiltinIndicator("obv20", bars[:20]); ok {
		t.Fatal("obv20 on 20 bars must fail closed")
	}
}

func TestAmihud(t *testing.T) {
	// 每日 |ret|=0.01, amount=1e8 → mean(|ret|/amount)=1e-10，×1e9 缩放后 = 0.1
	bars := make([]Bar, 0, 21)
	c := 10.0
	for i := 0; i < 21; i++ {
		nc := c * 1.01
		bars = append(bars, Bar{Open: c, High: nc, Low: c, Close: nc, Volume: 1e6, Amount: 1e8})
		c = nc
	}
	almost(t, "amihud20", mustCompute(t, "amihud20", bars), 0.1)
	if _, ok := ComputeBuiltinIndicator("amihud20", bars[:20]); ok {
		t.Fatal("amihud20 on 20 bars must fail closed")
	}
}

func TestAmplitudeFamily(t *testing.T) {
	// amplitude1 = (H-L)/prevClose；risingBars(40) 尾日 H-L=1, prevClose=48
	bars := risingBars(40)
	almost(t, "amplitude1", mustCompute(t, "amplitude1", bars), 1.0/48.0)
	if _, ok := ComputeBuiltinIndicator("amplitude1", bars[:1]); ok {
		t.Fatal("amplitude1 on 1 bar must fail closed")
	}
}

func TestClosePositionAndShadows(t *testing.T) {
	bars := risingBars(40)
	// 尾日(i=39) close=high=49, low=48, open=48.5, prevClose=48
	almost(t, "closepos", mustCompute(t, "closepos", bars), 1)
	almost(t, "upshadow", mustCompute(t, "upshadow", bars), 0)
	almost(t, "downshadow", mustCompute(t, "downshadow", bars), 0.5/48.0)
	flat := []Bar{{Close: 10, High: 10, Low: 10, Volume: 1e6, Amount: 1e7}}
	if _, ok := ComputeBuiltinIndicator("closepos", flat); ok {
		t.Fatal("closepos on H==L must fail closed")
	}
}

func TestOffHighLowFamilies(t *testing.T) {
	bars := risingBars(30) // close_29=39
	// prevhigh20（不含当日）= close_28=38 → offhigh20 = 39/38-1
	almost(t, "offhigh20", mustCompute(t, "offhigh20", bars), 39.0/38.0-1)
	// prevlow20 = close_9=19 → offlow20 = 39/19-1
	almost(t, "offlow20", mustCompute(t, "offlow20", bars), 39.0/19.0-1)
	if _, ok := ComputeBuiltinIndicator("offhigh20", bars[:20]); ok {
		t.Fatal("offhigh20 on 20 bars must fail closed (needs 21)")
	}
}

func TestUpStreakAndUpDays(t *testing.T) {
	bars := risingBars(30)
	almost(t, "upstreak", mustCompute(t, "upstreak", bars), 29)
	almost(t, "updays20", mustCompute(t, "updays20", bars), 1)
	broken := risingBars(30)
	last := broken[len(broken)-1]
	last.Close -= 2
	last.High = last.Close
	broken[len(broken)-1] = last
	almost(t, "upstreak(down day)", mustCompute(t, "upstreak", broken), 0)
	almost(t, "updays20(1 down)", mustCompute(t, "updays20", broken), 19.0/20.0)
	if _, ok := ComputeBuiltinIndicator("updays20", bars[:20]); ok {
		t.Fatal("updays20 on 20 bars must fail closed")
	}
}

// TestEveryIndicatorFamilyIsBuiltin 保证新指标族在 DSL 编译期被识别为内建，
// 否则会被当成外部特征依赖而不是可直接计算的内建指标。
func TestEveryIndicatorFamilyIsBuiltin(t *testing.T) {
	names := []string{
		"ret5", "ret120", "return1", "bias5", "bias20",
		"kdj_k", "kdj_d", "kdj_j", "boll_upper", "boll_mid", "boll_lower",
		"cci14", "willr20", "atr14", "atrp14", "volratio5", "obv20",
		"amihud20", "amplitude1", "closepos", "upshadow", "downshadow",
		"offhigh250", "offlow20", "upstreak", "updays20",
		"ema12", "ema26", "volatility5", "volatility60",
	}
	for _, name := range names {
		if !isBuiltinIndicator(name) {
			t.Errorf("%s must be a builtin indicator", name)
		}
	}
}
