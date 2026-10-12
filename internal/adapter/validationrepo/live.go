package validationrepo

// LiveSource 是项目红线的数据源实现：研究/验证永远直接读活表 kline 的
// 真实全量数据，不冻结快照、不使用副本。它同时实现三个端口：
//   - SnapshotStore 形状（List/GetByID/VerifyContent/CreateKlineSnapshot）——
//     供 methodseed/methodautomation/factorlab/paradigmspromote 现有装配直接替换；
//   - UniverseResolver（ResolveUniverse）；
//   - validation.BarProvider（LoadBars）。
//
// 所有以 snapshotID 命名的方法都会忽略该参数：任何 ID 返回的都是活表数据源
// （身份标识固定为 LiveSnapshotID），保证证据链里记录的数据源诚实可见。

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sjzsdu/tongstock/internal/backtest"
	"github.com/sjzsdu/tongstock/internal/paradigm"
	"github.com/sjzsdu/tongstock/internal/validation"
	"github.com/sjzsdu/tongstock/pkg/storage"
)

// LiveSnapshotID 是活表数据源在证据链中的固定身份标识。
const LiveSnapshotID = "live-kline"

// liveWindowYears 是研究/验证的默认数据窗口（年）。全历史回测（1990 起）
// 成本过高且远古市场结构与当前不可比，默认取最新 4 年。
const liveWindowYears = 4

// LiveSource 直读活表 kline 的全量数据源。
type LiveSource struct {
	store *storage.Storage

	mu     sync.Mutex
	cached *paradigm.DatasetSnapshot
}

// NewLiveSource 创建活表数据源。
func NewLiveSource(store *storage.Storage) *LiveSource {
	return &LiveSource{store: store}
}

// liveSnapshot 构造（并缓存）活表数据源的描述对象：窗口取最新 4 年，
// universe 为窗口内拥有足够日线的全部股票。
func (s *LiveSource) liveSnapshot() (*paradigm.DatasetSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil {
		return s.cached, nil
	}
	var minDate, maxDate string
	if err := s.store.DB().QueryRow(
		`SELECT MIN(date), MAX(date) FROM kline WHERE ktype=?`, KlineTypeDaily,
	).Scan(&minDate, &maxDate); err != nil || maxDate == "" {
		if err != nil {
			return nil, fmt.Errorf("probe live kline range: %w", err)
		}
		return nil, fmt.Errorf("live kline table is empty: run a market-wide daily sync first")
	}
	end, err := time.Parse("20060102", maxDate)
	if err != nil {
		return nil, fmt.Errorf("parse live kline max date %s: %w", maxDate, err)
	}
	start := end.AddDate(-liveWindowYears, 0, 0)
	codes, _, err := s.resolveLiveUniverse(start.Format("20060102"), 30, 0)
	if err != nil {
		return nil, err
	}
	snap := &paradigm.DatasetSnapshot{
		ID:              LiveSnapshotID,
		Version:         "live-kline-v1",
		DateRange:       paradigm.DateRange{Start: start.Format("2006-01-02"), End: end.Format("2006-01-02")},
		Universe:        codes,
		Market:          "A",
		PriceAdjustment: paradigm.PriceRaw,
		Description:     "活表全量数据源（项目红线：直接读 kline 真实全量，不冻结快照）",
		CreatedAt:       time.Now().UTC(),
	}
	s.cached = snap
	return snap, nil
}

// GetByID 返回活表数据源描述（忽略传入 ID）。
func (s *LiveSource) GetByID(id string) (*paradigm.DatasetSnapshot, error) {
	return s.liveSnapshot()
}

// VerifyContent 活表即数据源本体，无需校验（忽略传入 ID，恒成功）。
func (s *LiveSource) VerifyContent(snapshotID string) error { return nil }

// List 供 resolveSnapshot 的扫描路径使用：永远只返回活表数据源一项。
func (s *LiveSource) List(limit, offset int) ([]*paradigm.DatasetSnapshot, error) {
	if offset > 0 {
		return nil, nil
	}
	snap, err := s.liveSnapshot()
	if err != nil {
		return nil, err
	}
	return []*paradigm.DatasetSnapshot{snap}, nil
}

// CreateKlineSnapshot 按红线禁用：不再冻结任何快照。
func (s *LiveSource) CreateKlineSnapshot(snapshot *paradigm.DatasetSnapshot, ktype uint8) error {
	return fmt.Errorf("freezing snapshots is disabled: live kline is the single source of truth")
}

// ResolveUniverse 实现 UniverseResolver：活表内窗口期拥有 >=minBars 根日线的
// 全部股票；maxCodes>0 时按代码升序等步长抽样（与原快照语义一致）。
func (s *LiveSource) ResolveUniverse(ctx context.Context, snapshotID string, minBars, maxCodes int) ([]string, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	snap, err := s.liveSnapshot()
	if err != nil {
		return nil, 0, err
	}
	windowStart := normalizeDate(snap.DateRange.Start)
	codes, skipped, err := s.resolveLiveUniverse(windowStart, minBars, maxCodes)
	if err != nil {
		return nil, 0, err
	}
	if len(codes) == 0 {
		return nil, 0, fmt.Errorf("live kline has no code with >=%d bars since %s: run a market-wide daily sync first", minBars, windowStart)
	}
	return codes, skipped, nil
}

func (s *LiveSource) resolveLiveUniverse(windowStart string, minBars, maxCodes int) ([]string, int, error) {
	rows, err := s.store.DB().Query(
		`SELECT code, COUNT(*) FROM kline WHERE ktype=? AND date >= ? GROUP BY code ORDER BY code`,
		KlineTypeDaily, windowStart,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve live universe: %w", err)
	}
	defer rows.Close()
	var codes []string
	skipped := 0
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			return nil, 0, err
		}
		if n < minBars {
			skipped++
			continue
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if maxCodes > 0 && len(codes) > maxCodes {
		// 均匀抽样，避免只取代码序靠前的交易所偏置。
		stride := float64(len(codes)) / float64(maxCodes)
		sampled := make([]string, 0, maxCodes)
		for i := 0; i < maxCodes; i++ {
			sampled = append(sampled, codes[int(float64(i)*stride)])
		}
		codes = sampled
	}
	return codes, skipped, nil
}

// LoadBars 实现 validation.BarProvider：直读活表 kline（区间过滤在 SQL 完成，
// 区间前取一根真实前收盘，与冻结实现的 PreClose 语义一致）。
func (s *LiveSource) LoadBars(ctx context.Context, snapshotID, code, dateStart, dateEnd string) ([]validation.BacktestBar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if code == "" {
		return nil, fmt.Errorf("code is required")
	}
	start := normalizeDate(dateStart)
	end := normalizeDate(dateEnd)

	var prevClose float64
	if start != "" {
		if err := s.store.DB().QueryRow(
			`SELECT close FROM kline WHERE code=? AND ktype=? AND date < ? ORDER BY date DESC LIMIT 1`,
			code, KlineTypeDaily, start,
		).Scan(&prevClose); err != nil && err.Error() != "sql: no rows in result set" {
			return nil, fmt.Errorf("probe prev close %s: %w", code, err)
		}
	}

	query := `SELECT date, open, high, low, close, volume, amount FROM kline WHERE code=? AND ktype=?`
	args := []any{code, KlineTypeDaily}
	if start != "" {
		query += ` AND date >= ?`
		args = append(args, start)
	}
	if end != "" {
		query += ` AND date <= ?`
		args = append(args, end)
	}
	query += ` ORDER BY date`
	rows, err := s.store.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("load live kline %s: %w", code, err)
	}
	defer rows.Close()

	board := backtest.BoardForCode(code)
	bars := make([]validation.BacktestBar, 0, 1024)
	for rows.Next() {
		var date string
		var open, high, low, close, volume, amount float64
		if err := rows.Scan(&date, &open, &high, &low, &close, &volume, &amount); err != nil {
			return nil, err
		}
		if len(date) == 8 {
			date = date[:4] + "-" + date[4:6] + "-" + date[6:]
		}
		bar := validation.BacktestBar{
			Code: code, Date: date,
			Open: open, High: high, Low: low, Close: close,
			Volume: volume, Amount: amount, Board: board,
			PreClose: prevClose, Suspended: volume <= 0,
		}
		if !bar.Suspended && (bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Close <= 0) {
			return nil, fmt.Errorf("invalid live bar %s %s: non-positive price", code, bar.Date)
		}
		bars = append(bars, bar)
		prevClose = close
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("live kline %s has no bars in [%s,%s]: fail closed", code, dateStart, dateEnd)
	}
	return bars, nil
}

var (
	_ paradigmSnapshotStore = (*LiveSource)(nil)
	_ validation.BarProvider = (*LiveSource)(nil)
)

// paradigmSnapshotStore 汇总各研究服务对快照存储的方法集要求（超集）。
type paradigmSnapshotStore interface {
	List(limit, offset int) ([]*paradigm.DatasetSnapshot, error)
	GetByID(id string) (*paradigm.DatasetSnapshot, error)
	VerifyContent(snapshotID string) error
	CreateKlineSnapshot(snapshot *paradigm.DatasetSnapshot, ktype uint8) error
}
