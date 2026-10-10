package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sjzsdu/tongstock/pkg/config"
	"github.com/sjzsdu/tongstock/pkg/stockinfo"
	"github.com/sjzsdu/tongstock/pkg/storage"
	"github.com/sjzsdu/tongstock/pkg/watchlist"
	"github.com/spf13/cobra"
)

var (
	watchlistName    string
	watchlistGroup   string
	watchlistNote    string
	watchlistJSON    bool
	watchlistGroupFl string
)

// watchlistCmd 管理自选股列表（与 web 端 /api/watchlist 共用同一存储）。
var watchlistCmd = &cobra.Command{
	Use:   "watchlist",
	Short: "管理自选股列表",
	Long: `查看和管理自选股列表，数据与 web 端自选股页面完全同步。

不带子命令时等同于 list。

示例:
  tongstock watchlist                          # 列出全部自选股
  tongstock watchlist add 600519 002714        # 添加自选（自动补全名称）
  tongstock watchlist add 300750 --group 行业 --note "长线观察"
  tongstock watchlist remove 600519            # 移除自选
  tongstock watchlist group 600519 custom      # 修改分组
  tongstock watchlist note 600519 "止损位 15.00" # 修改备注`,
	RunE: runWatchlistList,
}

var watchlistAddCmd = &cobra.Command{
	Use:   "add <code> [code...]",
	Short: "添加自选股",
	Long: `添加一只或多只股票到自选列表。

代码支持 600519、sh600519、600519.SH 等写法；
名称优先用 --name，否则从本地 stockinfo 库自动补全，缺失时回退为代码本身。`,
	Args: cobra.MinimumNArgs(1),
	RunE: runWatchlistAdd,
}

var watchlistRemoveCmd = &cobra.Command{
	Use:     "remove <code> [code...]",
	Aliases: []string{"rm", "delete"},
	Short:   "移除自选股",
	Args:    cobra.MinimumNArgs(1),
	RunE:    runWatchlistRemove,
}

var watchlistGroupCmd = &cobra.Command{
	Use:   "group <code> <group>",
	Short: "修改自选股分组",
	Args:  cobra.ExactArgs(2),
	RunE:  runWatchlistGroup,
}

var watchlistNoteCmd = &cobra.Command{
	Use:   "note <code> <note>",
	Short: "修改自选股备注",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runWatchlistNote,
}

func init() {
	watchlistCmd.Flags().StringVarP(&watchlistGroupFl, "group", "g", "", "只列出指定分组的自选股")
	watchlistCmd.Flags().BoolVar(&watchlistJSON, "json", false, "以 JSON 输出")
	watchlistAddCmd.Flags().StringVar(&watchlistName, "name", "", "股票名称（默认从本地 stockinfo 库补全）")
	watchlistAddCmd.Flags().StringVar(&watchlistGroup, "group", "default", "分组")
	watchlistAddCmd.Flags().StringVar(&watchlistNote, "note", "", "备注")
	watchlistCmd.AddCommand(watchlistAddCmd, watchlistRemoveCmd, watchlistGroupCmd, watchlistNoteCmd)
	rootCmd.AddCommand(watchlistCmd)
}

// openWatchlistStore 打开与 server 相同的数据库并返回自选股存储。
func openWatchlistStore() (*watchlist.Store, *storage.Storage, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := storage.New(storage.Config{Driver: cfg.Database.Driver, DSN: cfg.Database.DSN})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("初始化存储失败: %w", err)
	}
	wl, err := watchlist.New(store)
	if err != nil {
		_ = store.Close()
		return nil, nil, nil, fmt.Errorf("初始化自选股失败: %w", err)
	}
	return wl, store, func() { _ = store.Close() }, nil
}

// normalizeWatchlistCode 归一化股票代码：去掉交易所前缀/后缀，校验 6 位数字。
func normalizeWatchlistCode(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if code == "" {
		return "", fmt.Errorf("代码为空")
	}
	// 去掉 600519.SH / 600519.SH / 600519~ 等后缀
	if idx := strings.IndexAny(code, ".~/"); idx > 0 {
		code = code[:idx]
	}
	// 去掉 sh/sz/bj 前缀
	lower := strings.ToLower(code)
	for _, prefix := range []string{"sh", "sz", "bj"} {
		if strings.HasPrefix(lower, prefix) && len(code) == len(prefix)+6 {
			code = code[len(prefix):]
			break
		}
	}
	if matched, _ := regexp.MatchString(`^\d{6}$`, code); !matched {
		return "", fmt.Errorf("无法识别的股票代码 %q（支持 600519 / sh600519 / 600519.SH 写法）", raw)
	}
	return code, nil
}

// resolveWatchlistName 解析股票名称：--name 优先，其次本地 stockinfo 库，最后回退为代码。
func resolveWatchlistName(store *storage.Storage, code, explicit string) string {
	if name := strings.TrimSpace(explicit); name != "" {
		return name
	}
	infoStore, err := stockinfo.New(store)
	if err == nil {
		if info, err := infoStore.GetByCode(code); err == nil && info != nil && strings.TrimSpace(info.Name) != "" {
			return strings.TrimSpace(info.Name)
		}
	}
	return code
}

func runWatchlistAdd(cmd *cobra.Command, args []string) error {
	wl, store, cleanup, err := openWatchlistStore()
	if err != nil {
		return err
	}
	defer cleanup()

	added, failed := 0, 0
	for _, raw := range args {
		code, err := normalizeWatchlistCode(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			failed++
			continue
		}
		stock := watchlist.WatchlistStock{
			Code:  code,
			Name:  resolveWatchlistName(store, code, watchlistName),
			Group: watchlistGroup,
			Note:  watchlistNote,
		}
		if err := wl.Upsert(stock); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s 写入失败: %v\n", code, err)
			failed++
			continue
		}
		existed, _ := wl.Exists(code)
		verb := "已添加"
		if existed {
			verb = "已更新"
		}
		fmt.Printf("✓ %s %s（分组 %s）%s\n", code, stock.Name, stock.Group, verb)
		added++
	}
	if added == 0 {
		return fmt.Errorf("没有成功添加任何自选股")
	}
	if failed > 0 {
		fmt.Printf("完成: 成功 %d，失败 %d\n", added, failed)
	}
	return nil
}

func runWatchlistRemove(cmd *cobra.Command, args []string) error {
	wl, _, cleanup, err := openWatchlistStore()
	if err != nil {
		return err
	}
	defer cleanup()

	removed, missing, failed := 0, 0, 0
	for _, raw := range args {
		code, err := normalizeWatchlistCode(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %v\n", err)
			failed++
			continue
		}
		exists, err := wl.Exists(code)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s 查询失败: %v\n", code, err)
			failed++
			continue
		}
		if !exists {
			fmt.Fprintf(os.Stderr, "- %s 不在自选中，跳过\n", code)
			missing++
			continue
		}
		if err := wl.Delete(code); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s 删除失败: %v\n", code, err)
			failed++
			continue
		}
		fmt.Printf("✓ 已移除 %s\n", code)
		removed++
	}
	if removed == 0 && failed == 0 {
		return fmt.Errorf("没有移除任何自选股")
	}
	if failed > 0 || missing > 0 {
		fmt.Printf("完成: 移除 %d，缺失 %d，失败 %d\n", removed, missing, failed)
	}
	return nil
}

func runWatchlistGroup(cmd *cobra.Command, args []string) error {
	code, err := normalizeWatchlistCode(args[0])
	if err != nil {
		return err
	}
	wl, _, cleanup, err := openWatchlistStore()
	if err != nil {
		return err
	}
	defer cleanup()
	if err := wl.UpdateGroup(code, strings.TrimSpace(args[1])); err != nil {
		return fmt.Errorf("更新分组失败: %w", err)
	}
	fmt.Printf("✓ %s 分组已更新为 %s\n", code, strings.TrimSpace(args[1]))
	return nil
}

func runWatchlistNote(cmd *cobra.Command, args []string) error {
	code, err := normalizeWatchlistCode(args[0])
	if err != nil {
		return err
	}
	note := strings.TrimSpace(strings.Join(args[1:], " "))
	wl, _, cleanup, err := openWatchlistStore()
	if err != nil {
		return err
	}
	defer cleanup()
	if err := wl.UpdateNote(code, note); err != nil {
		return fmt.Errorf("更新备注失败: %w", err)
	}
	fmt.Printf("✓ %s 备注已更新\n", code)
	return nil
}

func runWatchlistList(cmd *cobra.Command, args []string) error {
	wl, _, cleanup, err := openWatchlistStore()
	if err != nil {
		return err
	}
	defer cleanup()

	var stocks []watchlist.WatchlistStock
	if group := strings.TrimSpace(watchlistGroupFl); group != "" {
		stocks, err = wl.GetByGroup(group)
	} else {
		stocks, err = wl.GetAll()
	}
	if err != nil {
		return fmt.Errorf("读取自选股失败: %w", err)
	}

	if watchlistJSON {
		if stocks == nil {
			stocks = []watchlist.WatchlistStock{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stocks)
	}

	if len(stocks) == 0 {
		fmt.Println("自选列表为空，使用 tongstock watchlist add <code> 添加")
		return nil
	}

	fmt.Printf("自选股 %d 只:\n", len(stocks))
	lastGroup := ""
	for _, s := range stocks {
		group := s.Group
		if group == "" {
			group = "default"
		}
		name := s.Name
		if name == "" {
			name = "-"
		}
		if group != lastGroup {
			fmt.Printf("\n[%s]\n", group)
			lastGroup = group
		}
		line := fmt.Sprintf("  %s %s", s.Code, name)
		if s.Note != "" {
			line += fmt.Sprintf("  # %s", s.Note)
		}
		fmt.Println(line)
	}
	groupCounts, _ := wl.CountByGroup()
	if len(groupCounts) > 0 {
		parts := make([]string, 0, len(groupCounts))
		for g, c := range groupCounts {
			parts = append(parts, fmt.Sprintf("%s %d", g, c))
		}
		fmt.Printf("\n分组统计: %s\n", strings.Join(parts, " | "))
	}
	return nil
}
