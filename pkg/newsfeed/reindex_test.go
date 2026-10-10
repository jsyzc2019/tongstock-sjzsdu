package newsfeed

import (
	"context"
	"testing"
	"time"
)

// 关联规则升级后，存量 news_stock_ref 不会被重新抓取覆盖。重建必须：
//  1. 丢弃旧识别器的 2 字简称标题命中（name_hit @ 0.5）；
//  2. 按当前规则重算（弱别名降级、星号变体补召回）；
//  3. 数据源原生关联原样保留，且不降低数据源给定的置信度。
func TestRebuildRefsDropsLegacyTwoCharTitleHits(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	base := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

	legacy := newsAt("n1", "人社部：将研究出台服务业发展促就业文件 扩大服务业就业总量", "", base)
	stVariant := newsAt("n2", "ST发展披露整改进展", "", base)
	native := newsAt("n3", "江淮汽车增资至约22.54亿", "", base)
	if err := store.SaveNews(ctx, []*NewsItem{legacy, stVariant, native}); err != nil {
		t.Fatal(err)
	}

	// 直接写入存量行，模拟旧识别器与数据源在库里留下的关联。
	for _, r := range []struct {
		id, code, matchType string
		conf                float64
	}{
		{"n1", "000838", MatchNameHit, 0.5},
		{"n2", "000838", MatchNameHit, 0.5},
		{"n3", "600418", MatchNative, 1.0},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO news_stock_ref (news_id, code, match_type, confidence, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, r.id, r.code, r.matchType, r.conf, base.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}

	// 孤儿关联（news_id 已不在 news_items）应被顺带清理。
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO news_stock_ref (news_id, code, match_type, confidence, created_at)
		VALUES ('ghost-news', '000838', 'name_hit', 0.5, ?)
	`, base.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	store.SetEntityMatcher(NewEntityMatcher([]StockEntity{
		{Code: "000838", Name: "*ST发展"},
		{Code: "600418", Name: "江淮汽车"},
	}))

	changed, pruned, err := store.RebuildRefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("应修改 n1/n2 两条，实际 %d 条", changed)
	}
	if pruned != 1 {
		t.Fatalf("应清理 1 条孤儿关联，实际 %d 条", pruned)
	}

	// n1：泛词命中「发展」降为弱置信，低于默认阈值，进不了榜单。
	n1, err := store.loadRefs(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if len(n1) != 1 || n1[0].Code != "000838" {
		t.Fatalf("n1 关联异常: %+v", n1)
	}
	if n1[0].Confidence >= DefaultMinConfidence {
		t.Fatalf("n1 泛词命中应低于默认阈值，实际 %+v", n1[0])
	}

	// n2：文本写「ST发展」（无星号）也要按完整简称强命中。
	n2, err := store.loadRefs(ctx, "n2")
	if err != nil {
		t.Fatal(err)
	}
	if len(n2) != 1 || n2[0].Code != "000838" || n2[0].Confidence < 0.9 {
		t.Fatalf("n2 应补上星号变体强命中: %+v", n2)
	}

	// n3：原生关联保持不变，不被本地识别结果改写。
	n3, err := store.loadRefs(ctx, "n3")
	if err != nil {
		t.Fatal(err)
	}
	if len(n3) != 1 || n3[0].MatchType != MatchNative || n3[0].Confidence != 1.0 {
		t.Fatalf("n3 原生关联被改写: %+v", n3)
	}

	// 幂等：再次重建不应再有变更或重复清理。
	again, prunedAgain, err := store.RebuildRefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 || prunedAgain != 0 {
		t.Fatalf("重复重建应无变更，实际修改 %d 条、清理 %d 条", again, prunedAgain)
	}
}

// 过期清理必须连带删除关联，否则会留下永远读不到的孤儿行。
func TestDeleteExpiredNewsRemovesRefs(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	old := newsAt("old1", "旧闻", "", time.Now().AddDate(0, 0, -90))
	old.StockRefs = []StockRef{{Code: "600519", MatchType: MatchNative, Confidence: 1}}
	fresh := newsAt("fresh1", "新闻", "", time.Now())
	fresh.StockRefs = []StockRef{{Code: "600519", MatchType: MatchNative, Confidence: 1}}
	if err := store.SaveNews(ctx, []*NewsItem{old, fresh}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.DeleteExpiredNews(ctx, 30); err != nil {
		t.Fatal(err)
	}
	var refs int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM news_stock_ref WHERE news_id = 'old1'`).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if refs != 0 {
		t.Fatalf("过期新闻的关联应一并删除，实际残留 %d 条", refs)
	}
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM news_stock_ref WHERE news_id = 'fresh1'`).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if refs != 1 {
		t.Fatalf("未过期新闻的关联不应被删，实际 %d 条", refs)
	}
}
