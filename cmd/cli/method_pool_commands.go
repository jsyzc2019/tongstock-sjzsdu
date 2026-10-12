package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sjzsdu/tongstock/internal/adapter/marketsnapshotrepo"
	"github.com/sjzsdu/tongstock/internal/adapter/methodregistryrepo"
	"github.com/sjzsdu/tongstock/internal/adapter/selectionrepo"
	"github.com/sjzsdu/tongstock/internal/marketsnapshot"
	"github.com/sjzsdu/tongstock/internal/selection"
	"github.com/spf13/cobra"
)

var (
	methodPoolMethod string
	methodPoolOut    string
	methodPoolShow   string // all / in / triggered
)

// methodLibraryPoolCmd 列出一个方法在当前冻结快照上的适用股票池：
// 逐只解析 scope（股票池/市值/户均持股/ST 等）并判定当日是否触发入场规则。
var methodLibraryPoolCmd = &cobra.Command{
	Use:   "pool",
	Short: "列出一个方法当前适用的股票池（scope 全量判定 + 当日触发）",
	Long: `按最新冻结快照逐只解析方法的 scope，输出池内全部股票与关键属性
（流通市值、户均持股、收盘价），并标注当日入场规则是否触发。

这是只读诊断视图，不修改方法库。默认只打印池内股票，--show all 包含被排除的。
用法示例：
  tongstock method library pool --method method-9626afeb68e7b3c3
  tongstock method library pool --method method-9626afeb68e7b3c3 --export pool.csv`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
		defer cancel()

		store, err := openValidationStorage()
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Migrate(); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}

		methodRepo, err := methodregistryrepo.New(store)
		if err != nil {
			return err
		}
		snapshots, err := marketsnapshotrepo.New(store)
		if err != nil {
			return err
		}
		runs, err := selectionrepo.New(store)
		if err != nil {
			return err
		}
		engine, err := selection.NewEngine(snapshots, methodRepo, runs)
		if err != nil {
			return err
		}
		// 与 serverapp 相同的特征快照 ensurer：库内旧快照缺新 scope 特征时就地重建。
		engine.SetFeatureEnsurer(&marketsnapshot.FeatureEnsurer{
			Builder: marketsnapshot.NewBuilder(
				marketsnapshotrepo.NewSQLiteUniverseProvider(store),
				marketsnapshotrepo.NewSQLiteWatermarkProvider(store),
				marketsnapshotrepo.NewSQLiteTradingCalendar(store),
			),
			Snapshots: snapshots,
			Features:  marketsnapshotrepo.NewSQLiteFeatureEngine(store),
		})
		// 展示名称补全（快照重载不带 name）。
		engine.SetPoolNamer(marketsnapshotrepo.NewStockNameReader(store))

		latest := ""
		items, err := snapshots.ListMarketSnapshots("", "", "ready")
		if err != nil {
			return err
		}
		for _, x := range items {
			if x != nil && x.Frozen {
				latest = x.ID
				break
			}
		}
		if latest == "" {
			return fmt.Errorf("没有已冻结就绪的市场快照，请先 tongstock market 冻结快照")
		}

		pool, err := engine.MethodPool(ctx, selection.Request{
			MarketSnapshotID: latest,
			MethodIDs:        []string{methodPoolMethod},
		})
		if err != nil {
			return err
		}

		show := methodPoolShow
		if show == "" {
			show = "in"
		}
		if methodPoolOut != "" {
			if err := writePoolCSV(pool, methodPoolOut); err != nil {
				return err
			}
			fmt.Printf("已导出 %d 行到 %s\n", len(pool.Entries), methodPoolOut)
		}

		fmt.Printf("方法: %s | 快照: %s (%s) | 扫描 %d 只 → 池内 %d 只，当日触发 %d 只\n\n",
			pool.MethodID, pool.SnapshotDate, pool.SnapshotID, pool.Total, pool.InScopeCount, pool.TriggeredCount)
		fmt.Printf("%-8s %-10s %10s %12s %10s %-s\n", "代码", "名称", "收盘", "市值(亿)", "户均(股)", "状态")
		printed := 0
		for _, e := range pool.Entries {
			status := ""
			switch {
			case e.InScope && e.Triggered:
				status = "触发"
			case e.InScope:
				status = "池内"
			default:
				status = "排除"
			}
			if show == "in" && !e.InScope {
				continue
			}
			if show == "triggered" && !(e.InScope && e.Triggered) {
				continue
			}
			detail := e.ReasonCode
			if e.Detail != "" {
				detail = e.Detail
			}
			fmt.Printf("%-8s %-10s %10.2f %12.1f %12.0f %-s %s\n",
				e.Code, e.Name, e.Close, e.MarketCap, e.AvgSharesPerHolder, status, detail)
			printed++
			if printed >= 500 && show != "all" {
				break
			}
		}
		if printed < len(pool.Entries) {
			fmt.Printf("\n（仅显示前 %d 行，共 %d 行；--export 全量导出 CSV）\n", printed, len(pool.Entries))
		}
		return nil
	},
}

// writePoolCSV 全量导出股票池判定结果。
func writePoolCSV(pool *selection.MethodPool, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"code", "name", "board", "in_scope", "triggered", "close", "market_cap_yi", "avg_shares_per_holder", "reason_code", "detail"}); err != nil {
		return err
	}
	for _, e := range pool.Entries {
		if err := w.Write([]string{
			e.Code, e.Name, e.Board,
			strconv.FormatBool(e.InScope), strconv.FormatBool(e.Triggered),
			fmt.Sprintf("%.4f", e.Close), fmt.Sprintf("%.2f", e.MarketCap),
			fmt.Sprintf("%.0f", e.AvgSharesPerHolder), e.ReasonCode, e.Detail,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func init() {
	methodLibraryPoolCmd.Flags().StringVar(&methodPoolMethod, "method", "", "方法 ID（必填）")
	methodLibraryPoolCmd.Flags().StringVar(&methodPoolOut, "export", "", "全量导出 CSV 路径（可选）")
	methodLibraryPoolCmd.Flags().StringVar(&methodPoolShow, "show", "in", "显示范围: in（池内）/ triggered（当日触发）/ all（含排除）")
	_ = methodLibraryPoolCmd.MarkFlagRequired("method")
	methodLibraryCmd.AddCommand(methodLibraryPoolCmd)
}
