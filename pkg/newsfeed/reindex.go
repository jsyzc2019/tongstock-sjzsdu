package newsfeed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"
)

// legacyWeakNameConfidence 是旧版识别器给「2 字简称标题命中」的置信度。
// 新规则把这类命中降为 weakNameConfidence（低于默认阈值 0.5），但存量
// news_stock_ref 不会被重新抓取覆盖——数据源只返回最新一批新闻，老条目
// 的关联只在自己被重新 SaveNews 时才会重算。因此重建时要先丢弃这个
// 遗留值，再按当前规则重算。现行识别器与所有数据源都不再产生 0.5。
const legacyWeakNameConfidence = 0.5

// RebuildRefs 用当前实体识别器重建库内全部新闻-股票关联。
// 返回被修改的新闻条数与顺带清理的孤儿关联行数。识别规则升级后调用
// （CLI: tongstock news reindex）。
//
// 重建语义与落库时的 resolveRefs 保持一致，只额外丢弃遗留脏值：
//   - 数据源原生关联（native）与数据源给定的置信度原样保留为种子；
//   - 旧识别器写入的 2 字简称标题命中（name_hit @ 0.5）一律丢弃；
//   - 其余行作为种子与重算结果按「取高」合并，绝不降低数据源给定的置信度。
//
// 另外先清理孤儿关联（news_id 已不在 news_items 的行）：读路径全部
// JOIN news_items，这些行永远读不到，只会越积越多。
func (s *SQLiteStore) RebuildRefs(ctx context.Context) (int, int, error) {
	if s.matcher == nil || s.matcher.Empty() {
		return 0, 0, errors.New("实体识别器未初始化，无法重建关联")
	}

	pruned, err := s.pruneOrphanRefs(ctx)
	if err != nil {
		return 0, 0, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM news_items ORDER BY id`)
	if err != nil {
		return 0, pruned, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, pruned, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, pruned, err
	}
	rows.Close()

	changed := 0
	for _, id := range ids {
		ok, err := s.rebuildOneRef(ctx, id)
		if err != nil {
			return changed, pruned, err
		}
		if ok {
			changed++
		}
	}
	return changed, pruned, nil
}

// pruneOrphanRefs 删除 news_id 已不在 news_stock_ref 的关联行，返回删除行数。
func (s *SQLiteStore) pruneOrphanRefs(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM news_stock_ref WHERE news_id NOT IN (SELECT id FROM news_items)
	`)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

// rebuildOneRef 重建单条新闻的关联，返回是否有实际变更。
func (s *SQLiteStore) rebuildOneRef(ctx context.Context, id string) (bool, error) {
	item, err := s.loadRefSource(ctx, id)
	if err != nil {
		return false, err
	}
	if item == nil {
		return false, nil
	}
	existing, err := s.loadRefs(ctx, id)
	if err != nil {
		return false, err
	}

	seeds := make([]StockRef, 0, len(existing))
	for _, r := range existing {
		// 丢弃旧识别器的 2 字简称标题命中遗留值，交给当前规则重算；
		// 其余（native、数据源给定置信度、仍有效的本地命中）全部保留。
		if r.MatchType == MatchNameHit && math.Abs(r.Confidence-legacyWeakNameConfidence) < 1e-9 {
			continue
		}
		seeds = append(seeds, r)
	}
	item.StockRefs = seeds
	next := s.resolveRefs(item)

	if sameStockRefs(existing, next) {
		return false, nil
	}

	stocks := make([]string, 0, len(next))
	for _, r := range next {
		stocks = append(stocks, r.Code)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_stock_ref WHERE news_id = ?`, id); err != nil {
		return false, err
	}
	now := time.Now().Format(time.RFC3339)
	for _, r := range next {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO news_stock_ref (news_id, code, match_type, confidence, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, id, r.Code, r.MatchType, r.Confidence, now); err != nil {
			return false, err
		}
	}
	// related_stocks 与 news_stock_ref 必须保持一致（见 SaveNews）。
	if err := setRelatedStocks(ctx, tx, id, stocks, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// loadRefSource 读取重建关联所需的最小新闻字段。
func (s *SQLiteStore) loadRefSource(ctx context.Context, id string) (*NewsItem, error) {
	var it NewsItem
	var summary, content string
	err := s.db.QueryRowContext(ctx, `
		SELECT title, COALESCE(summary, ''), COALESCE(content, '')
		FROM news_items WHERE id = ?
	`, id).Scan(&it.Title, &summary, &content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	it.ID = id
	it.Summary = summary
	it.Content = content
	return &it, nil
}

// setRelatedStocks 同步 news_items.related_stocks 与 updated_at。
func setRelatedStocks(ctx context.Context, tx *sql.Tx, id string, stocks []string, now string) error {
	if stocks == nil {
		stocks = []string{}
	}
	encoded, err := json.Marshal(stocks)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE news_items SET related_stocks = ?, updated_at = ? WHERE id = ?
	`, string(encoded), now, id)
	return err
}

// sameStockRefs 按 (code, match_type, confidence) 比较两组关联。
func sameStockRefs(a, b []StockRef) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]StockRef(nil), a...)
	sb := append([]StockRef(nil), b...)
	sortRefsByCode(sa)
	sortRefsByCode(sb)
	for i := range sa {
		if sa[i].Code != sb[i].Code || sa[i].MatchType != sb[i].MatchType ||
			math.Abs(sa[i].Confidence-sb[i].Confidence) > 1e-9 {
			return false
		}
	}
	return true
}

func sortRefsByCode(refs []StockRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Code != refs[j].Code {
			return refs[i].Code < refs[j].Code
		}
		return refs[i].MatchType < refs[j].MatchType
	})
}

// RebuildRefs 用当前实体识别器重建库内关联，返回被修改的新闻条数与
// 清理的孤儿关联行数。识别规则升级后调用：重新抓取只会覆盖数据源最新
// 返回的一批新闻，存量数据必须显式重建。
func (s *Service) RebuildRefs(ctx context.Context) (int, int, error) {
	if s.directory != nil && s.matcher == nil {
		if err := s.RefreshEntities(ctx); err != nil {
			return 0, 0, err
		}
	}
	return s.store.RebuildRefs(ctx)
}
