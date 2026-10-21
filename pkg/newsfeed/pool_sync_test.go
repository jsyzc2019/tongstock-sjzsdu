package newsfeed

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

// sortedCodes 返回排序后的代码切片，便于并发补抓后的无序断言。
func sortedCodes(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

// 主路径：GlobalSync 落库全局流后，对命中代码补 FetchByStock，
// 补抓进来的新闻直接进入 topic 聚合语料。
func TestGlobalSyncPoolFetchesHitCodes(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	feed := &fakeFeed{
		name:  SourceEastMoney,
		items: []*NewsItem{newsFor("600519", "全局新闻-茅台")},
		byCode: map[string][]*NewsItem{
			"600519": {newsFor("600519", "定向新闻-茅台")},
		},
	}
	svc, err := NewService(store, []Feed{feed}, testNames)
	if err != nil {
		t.Fatal(err)
	}

	n, degraded := svc.GlobalSync(ctx)
	if n != 1 || len(degraded) != 0 {
		t.Fatalf("全局同步应成功: n=%d degraded=%+v", n, degraded)
	}
	got := feed.fetchedCodes()
	if len(got) != 1 || got[0] != "600519" {
		t.Fatalf("应对全局流命中代码补抓, got %v", got)
	}

	// 补抓的定向新闻落库后，topic 聚合的提及数应从 1 变 2。
	res, err := svc.HotStockTopics(ctx, HotTopicsRequest{Mode: NewsCacheOnly})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Mentions != 2 {
		t.Fatalf("补抓新闻应计入 topic: %+v", res.Items)
	}
}

// 补抓股票数受 Limit 上限约束；榜单/监控池（provider）只在预算有余时补位。
func TestGlobalSyncPoolRespectsLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	feed := &fakeFeed{
		name: SourceEastMoney,
		items: []*NewsItem{
			newsFor("600519", "全局-茅台"),
			newsFor("000001", "全局-平安"),
			newsFor("000002", "全局-万科"),
			newsFor("000004", "全局-国华"),
		},
	}
	svc, _ := NewService(store, []Feed{feed}, testNames)
	svc.SetStockPoolSync(StockPoolSyncConfig{Enabled: true, Limit: 2})
	svc.SetStockPoolProvider(func(context.Context) ([]string, error) {
		return []string{"000001"}, nil
	})

	if _, degraded := svc.GlobalSync(ctx); len(degraded) != 0 {
		t.Fatalf("degraded=%+v", degraded)
	}
	got := sortedCodes(feed.fetchedCodes())
	if len(got) != 2 || got[0] != "000001" || got[1] != "600519" {
		t.Fatalf("Limit=2 应只补抓前两只（首见顺序），got %v", got)
	}
}

// 命中代码不足上限时，榜单/监控池补足预算；两者取并集仍受上限约束。
func TestGlobalSyncPoolProviderFillsBudget(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	feed := &fakeFeed{
		name:  SourceEastMoney,
		items: []*NewsItem{newsFor("600519", "全局-茅台")},
	}
	svc, _ := NewService(store, []Feed{feed}, testNames)
	svc.SetStockPoolSync(StockPoolSyncConfig{Enabled: true, Limit: 3})
	svc.SetStockPoolProvider(func(context.Context) ([]string, error) {
		return []string{"000001", "000002", "0000000"}, nil // 最后一个非法，应被丢弃
	})

	if _, degraded := svc.GlobalSync(ctx); len(degraded) != 0 {
		t.Fatalf("degraded=%+v", degraded)
	}
	got := sortedCodes(feed.fetchedCodes())
	want := []string{"000001", "000002", "600519"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("并集应为 %v, got %v", want, got)
	}
}

// provider 出错只当没有补充候选，不阻塞、不降级。
func TestGlobalSyncPoolProviderErrorIgnored(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	feed := &fakeFeed{
		name:  SourceEastMoney,
		items: []*NewsItem{newsFor("600519", "全局-茅台")},
	}
	svc, _ := NewService(store, []Feed{feed}, testNames)
	svc.SetStockPoolSync(StockPoolSyncConfig{Enabled: true, Limit: 5})
	svc.SetStockPoolProvider(func(context.Context) ([]string, error) {
		return nil, errors.New("自选股表不可用")
	})

	if _, degraded := svc.GlobalSync(ctx); len(degraded) != 0 {
		t.Fatalf("provider 失败不应进入降级: %+v", degraded)
	}
	got := feed.fetchedCodes()
	if len(got) != 1 || got[0] != "600519" {
		t.Fatalf("provider 失败应回落到全局命中, got %v", got)
	}
}

// 开关关闭后回到旧行为：GlobalSync 只抓全局流。
func TestGlobalSyncPoolDisabled(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	feed := &fakeFeed{
		name:  SourceEastMoney,
		items: []*NewsItem{newsFor("600519", "全局-茅台")},
	}
	svc, _ := NewService(store, []Feed{feed}, testNames)
	svc.SetStockPoolSync(StockPoolSyncConfig{Enabled: false})

	if _, degraded := svc.GlobalSync(ctx); len(degraded) != 0 {
		t.Fatalf("degraded=%+v", degraded)
	}
	if got := feed.fetchedCodes(); len(got) != 0 {
		t.Fatalf("关闭补抓后不应调用 FetchByStock, got %v", got)
	}
}

// 单股抓取卡死时由超时预算兜底：总耗时有上界，且失败不进入降级。
func TestGlobalSyncPoolBoundedByTimeout(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	feed := &fakeFeed{
		name:  SourceEastMoney,
		items: []*NewsItem{newsFor("600519", "全局-茅台")},
		block: true,
	}
	svc, _ := NewService(store, []Feed{feed}, testNames)
	svc.SetStockPoolSync(StockPoolSyncConfig{
		Enabled:          true,
		Limit:            1,
		Concurrency:      1,
		PerStockTimeout:  150 * time.Millisecond,
		PerSourceTimeout: 50 * time.Millisecond,
	})

	start := time.Now()
	n, degraded := svc.GlobalSync(ctx)
	elapsed := time.Since(start)

	if n != 1 {
		t.Fatalf("全局同步本身应成功, n=%d", n)
	}
	if len(degraded) != 0 {
		t.Fatalf("补抓失败不应进入降级: %+v", degraded)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("补抓应受超时预算约束, elapsed=%s", elapsed)
	}
	if got := feed.fetchedCodes(); len(got) != 1 {
		t.Fatalf("补抓应被触发（随后被超时终止）, got %v", got)
	}
}

// 环境变量覆盖：headline 条数与补抓开关。
func TestTopicEnvOverrides(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	base := time.Date(2026, 9, 17, 10, 0, 0, 0, time.Local)
	for i := 0; i < 3; i++ {
		seedTopicNews(t, ctx, store, "n"+string(rune('a'+i)), "新闻", "600519", MatchNative, 1.0, base.Add(time.Duration(i)*time.Hour), 50+i)
	}
	feed := &fakeFeed{
		name:  SourceEastMoney,
		items: []*NewsItem{newsFor("600519", "全局-茅台")},
	}

	t.Setenv(EnvTopicHeadlineLimit, "1")
	t.Setenv(EnvTopicPoolSync, "0")

	svc, err := NewService(store, []Feed{feed}, testNames)
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.HotStockTopics(ctx, HotTopicsRequest{Date: "2026-09-17", Mode: NewsCacheOnly})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || len(got.Items[0].Headlines) != 1 {
		t.Fatalf("环境变量应把 headline 限制为 1: %+v", got.Items)
	}

	if _, _ = svc.GlobalSync(ctx); len(feed.fetchedCodes()) != 0 {
		t.Fatal("TONGSTOCK_TOPIC_POOL_SYNC=0 应关闭补抓")
	}
}
