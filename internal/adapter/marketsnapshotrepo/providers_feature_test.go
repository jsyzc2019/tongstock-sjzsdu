package marketsnapshotrepo_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sjzsdu/tongstock/internal/adapter/marketsnapshotrepo"
	"github.com/sjzsdu/tongstock/internal/marketsnapshot"
	"github.com/sjzsdu/tongstock/pkg/storage"
)

// featureTestDB 建一个带 260 根真实形状日线（单调上涨）+ stockinfo 的库，
// 用于验证「默认特征集里声明的每个特征都真的能算出来」。
func featureTestDB(t *testing.T) *storage.Storage {
	t.Helper()
	store, err := storage.New(storage.Config{Driver: "sqlite3", DSN: filepath.Join(t.TempDir(), "feat.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO stockinfo
		(code, name, exchange, price, market_cap, total_market_cap, turnover_rate, liu_tong_gu_ben, zong_gu_ben, updated_at)
		VALUES ('000001','平安银行','SZ', 10.0, 1200, 2400, 1.5, 1200000, 2400000, 1700000000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO finance_snapshot (code, payload, source_updated_at, updated_at)
		VALUES ('000001', '{"GuDongRenShu":500000}', 1700000000, 1700000000)`); err != nil {
		t.Fatal(err)
	}
	stmt, err := store.DB().Prepare(`INSERT INTO kline
		(code, ktype, date, open, high, low, close, volume, amount) VALUES (?, 9, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	base := time.Date(2023, 1, 3, 0, 0, 0, 0, time.Local)
	for i := 0; i < 260; i++ {
		d := base.AddDate(0, 0, i).Format("20060102")
		c := 10 + 0.05*float64(i)
		if _, err := stmt.Exec("000001", d, c-0.1, c+0.2, c-0.3, c, 1e6+1000*float64(i), c*1e6); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// TestEveryDefaultDslFeatureIsMaterializable 是 Phase 1 的验收红线：特征目录
// （DefaultDslFeatures）里声明的每一个特征，物化引擎都必须能在真实 kline 上
// 算出值。声明了却算不出来的特征会让引用它的方法整批 insufficient_data——
// 正是 gap_pct 卡点的同类问题，这条测试把它锁死在目录层。
func TestEveryDefaultDslFeatureIsMaterializable(t *testing.T) {
	store := featureTestDB(t)
	fe := marketsnapshotrepo.NewSQLiteFeatureEngine(store)

	features := marketsnapshot.DefaultDslFeatures()
	if len(features) < 60 {
		t.Fatalf("DefaultDslFeatures 只有 %d 个，Phase 1 目标是 ≥60（地基要厚）", len(features))
	}

	values, err := fe.Compute("2023-12-29", []string{"000001"}, features)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	perCode := values["000001"]
	if perCode == nil {
		t.Fatal("no values for 000001")
	}
	seen := map[string]bool{}
	for name := range perCode {
		seen[name] = true
	}
	for _, f := range features {
		if !seen[f.Name] {
			t.Errorf("特征 %s(%s) 声明在 DefaultDslFeatures 但物化引擎算不出值", f.Name, f.Category)
		}
	}
}

// TestLatestSelectionScopeFeaturesComeFromStockInfo 验证 scope 类特征（当前
// 快照信息，无 point-in-time 序列）从 stockinfo 取值而非 kline 计算。
func TestLatestSelectionScopeFeaturesComeFromStockInfo(t *testing.T) {
	store := featureTestDB(t)
	fe := marketsnapshotrepo.NewSQLiteFeatureEngine(store)

	var scope []marketsnapshot.FeatureSpec
	for _, f := range marketsnapshot.LatestSelectionFeatures() {
		if f.Category == "scope" {
			scope = append(scope, f)
		}
	}
	if len(scope) < 3 {
		t.Fatalf("LatestSelectionFeatures 的 scope 特征只有 %d 个，至少应有 market_cap/turnover_rate/total_market_cap", len(scope))
	}

	values, err := fe.Compute("2023-12-29", []string{"000001"}, scope)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	perCode := values["000001"]
	for _, f := range scope {
		if _, ok := perCode[f.Name]; !ok {
			t.Errorf("scope 特征 %s 未从 stockinfo 物化", f.Name)
		}
	}
	if perCode["market_cap"] != 1200 {
		t.Fatalf("market_cap = %v, want 1200", perCode["market_cap"])
	}
	if perCode["turnover_rate"] != 1.5 {
		t.Fatalf("turnover_rate = %v, want 1.5", perCode["turnover_rate"])
	}
	// 户均持股 = 流通股本(万股)×10000 / 股东户数 = 1200000×10000/500000 = 24000 股/户。
	if perCode["avg_shares_per_holder"] != 24000 {
		t.Fatalf("avg_shares_per_holder = %v, want 24000", perCode["avg_shares_per_holder"])
	}
}
