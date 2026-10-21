package newsfeed

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// StockPoolProvider 返回候选池里的补充代码（榜单/监控池，如自选股）。
// 返回错误时视为本轮没有补充候选，补抓主流程照常继续。
type StockPoolProvider func(ctx context.Context) ([]string, error)

// SetStockPoolSync 覆盖候选股池补抓配置。零值字段会被规范化为默认值，
// Enabled=false 可一键关闭回到旧行为。
func (s *Service) SetStockPoolSync(cfg StockPoolSyncConfig) {
	s.poolCfg = cfg.normalize()
}

// SetStockPoolProvider 注入榜单/监控池候选来源。与全局流命中代码取并集，
// 仍受 Limit 上限约束。nil 表示只用全局流命中代码。
func (s *Service) SetStockPoolProvider(fn StockPoolProvider) {
	s.poolProvider = fn
}

// syncStockPool 对候选股池补一轮 FetchByStock 定向抓取。
//
// 入参 hits 是本轮全局流落库后命中的股票代码（含实体识别补充的关联）。
// 补抓约束：并发受限、每源与每股都有超时、单股失败只跳过——绝不影响
// GlobalSync 的返回值与降级列表，也不改变其错误语义。
func (s *Service) syncStockPool(ctx context.Context, hits []string) {
	cfg := s.poolCfg
	if !cfg.Enabled || len(s.feeds) == 0 {
		return
	}
	cfg = cfg.normalize()

	pool := s.buildStockPool(ctx, hits, cfg.Limit)
	if len(pool) == 0 {
		return
	}

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for _, code := range pool {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			s.fetchStockForPool(ctx, code, cfg)
		}(code)
	}
	wg.Wait()
}

// fetchStockForPool 在股票级与源级双重超时预算内逐源定向抓取。
// 所有失败（超时、源错误、落库错误）静默跳过：补抓是增强路径，不是主流程。
func (s *Service) fetchStockForPool(ctx context.Context, code string, cfg StockPoolSyncConfig) {
	stockCtx, cancel := context.WithTimeout(ctx, cfg.PerStockTimeout)
	defer cancel()

	for _, f := range s.feeds {
		if f == nil || stockCtx.Err() != nil {
			return
		}
		srcCtx, scancel := context.WithTimeout(stockCtx, cfg.PerSourceTimeout)
		items, err := f.FetchByStock(srcCtx, code)
		scancel()
		if err != nil || len(items) == 0 {
			continue
		}
		if err := s.store.SaveNews(stockCtx, items); err != nil {
			continue
		}
	}
}

// buildStockPool 合并全局流命中代码与榜单/监控池代码，去重并截断到 limit。
// 排序：全局流命中按提及频次降序（同频保持首次出现顺序），榜单/监控池补位——
// 榜单服务的是「未被单独浏览过的热点股」，应当优先占用补抓预算。
func (s *Service) buildStockPool(ctx context.Context, hits []string, limit int) []string {
	freq := make(map[string]int)
	order := make([]string, 0, len(hits))
	for _, raw := range hits {
		code := normalizePoolCode(raw)
		if code == "" {
			continue
		}
		if _, seen := freq[code]; !seen {
			order = append(order, code)
		}
		freq[code]++
	}
	sort.SliceStable(order, func(i, j int) bool {
		return freq[order[i]] > freq[order[j]]
	})

	out := make([]string, 0, limit)
	seen := make(map[string]bool, limit)
	add := func(code string) {
		if len(out) >= limit {
			return
		}
		if code == "" || seen[code] {
			return
		}
		seen[code] = true
		out = append(out, code)
	}
	for _, code := range order {
		add(code)
		if len(out) >= limit {
			return out
		}
	}

	if s.poolProvider != nil && len(out) < limit {
		extra, err := s.poolProvider(ctx)
		if err == nil {
			for _, raw := range extra {
				add(normalizePoolCode(raw))
				if len(out) >= limit {
					break
				}
			}
		}
	}
	return out
}

// appendPoolHits 收集本轮落库新闻命中的股票代码。RelatedStocks 在 SaveNews
// 里已由 resolveRefs 回填（数据源原生关联 + 实体识别），这里直接读取。
func appendPoolHits(hits []string, items []*NewsItem) []string {
	for _, item := range items {
		if item == nil {
			continue
		}
		hits = append(hits, item.RelatedStocks...)
	}
	return hits
}

// normalizePoolCode 只接受 6 位纯数字代码，其余一律丢弃（不猜测）。
func normalizePoolCode(raw string) string {
	code := strings.TrimSpace(raw)
	if len(code) != 6 {
		return ""
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return code
}
