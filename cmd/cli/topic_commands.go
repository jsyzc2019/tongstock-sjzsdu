package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sjzsdu/tongstock/pkg/newsfeed"
	"github.com/spf13/cobra"
)

var (
	topicTop    int
	topicJSON   bool
	topicWide   bool
	topicMinCfg float64
)

// topicCmd 从新闻流中聚合出指定日期（默认今天）的热门股票榜单。
var topicCmd = &cobra.Command{
	Use:   "topic [date]",
	Short: "查看指定日期新闻里的热门股票",
	Long: `从各大新闻站点抓取指定日期的全局资讯，聚合出当日被新闻提及最多的热门股票。

日期参数可省略（默认今天），支持 2006-01-02 与 20060102 两种写法。
topic 是从媒体新闻中聚合热点，与是否交易日无关。

数据来自东方财富、财联社、证券时报、21世纪经济报道、第一财经、新浪财经、腾讯财经，
关联通过实体识别与数据源原生标注完成；
只统计标题命中或数据源原生关联的强相关内容，正文偶然提及不会计入榜单。

一致性遵循与其他命令相同的语义：
  require_fresh（默认）  必要时抓取，源全部失败则报错
  allow_stale            源失败时返回库内已有数据
  cache_only             只读库，不访问网络`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTopic,
}

func init() {
	topicCmd.Flags().IntVarP(&topicTop, "top", "n", 10, "返回榜单条数")
	topicCmd.Flags().BoolVar(&topicJSON, "json", false, "以 JSON 输出")
	topicCmd.Flags().BoolVar(&topicWide, "wide", false, "输出完整明细（关联新闻标题与来源）")
	topicCmd.Flags().Float64Var(&topicMinCfg, "min-confidence", 0, "关联置信度下限 [0,1]，0 表示用默认值 0.5")
}

func runTopic(cmd *cobra.Command, args []string) error {
	date := ""
	if len(args) > 0 {
		date = strings.TrimSpace(args[0])
	}
	if topicMinCfg < 0 || topicMinCfg > 1 {
		return fmt.Errorf("--min-confidence 必须在 [0,1] 之间")
	}

	svc, cleanup, err := dialNewsService()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
	defer cancel()

	result, err := svc.HotStockTopics(ctx, newsfeed.HotTopicsRequest{
		Date:          date,
		Top:           topicTop,
		Mode:          cliNewsMode(),
		ForceRefresh:  forceRefresh,
		MinConfidence: topicMinCfg,
	})
	if err != nil {
		return err
	}

	if topicJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	printTopics(result)
	return nil
}

func printTopics(r *newsfeed.HotTopicResult) {
	if r.Fallback {
		fmt.Printf("%s 不是交易日，已回溯到最近交易日 %s\n", dim(r.Date), r.TradingDate)
	}

	title := "热门股票"
	if r.Date != r.TradingDate {
		title += fmt.Sprintf("（%s）", r.TradingDate)
	}

	if len(r.Items) == 0 {
		fmt.Printf("%s: 没有可用的数据\n", bold(title))
		if r.Message != "" {
			fmt.Printf("  %s\n", r.Message)
		}
		for _, d := range r.Degraded {
			fmt.Printf("  %s %s 不可用: %s\n", yellow("数据源"), d.Source, d.Error)
		}
		return
	}

	statusLabel := map[string]string{
		"ok":                "",
		"stale":             yellow("（部分数据源不可用，展示库内已有数据）"),
		"insufficient_data": yellow("（数据不足）"),
	}[r.Status]
	fmt.Printf("%s %s %s\n", bold(title), dim("日期="+r.Date), statusLabel)
	if r.SyncedCount > 0 {
		fmt.Printf("  本次抓取入库 %d 条新闻\n", r.SyncedCount)
	}
	fmt.Println()

	for i, it := range r.Items {
		name := it.Name
		if name == "" {
			name = "-"
		}
		fmt.Printf("%s %s %s  提及 %s 次  热度 %s\n",
			bold(fmt.Sprintf("%2d.", i+1)), cyan(it.Code), bold(name),
			fmt.Sprintf("%d", it.Mentions), hotScoreStyle(it.HotScore))
		if topicWide {
			for _, h := range it.Headlines {
				fmt.Printf("      %s %s\n",
					dim(fmt.Sprintf("· [%s] [%s]", h.PublishTime.Format("01-02 15:04"), h.Source)),
					h.Title)
				if h.Summary != "" {
					fmt.Printf("        %s\n", dim(h.Summary))
				}
				if h.MatchReason != "" {
					fmt.Printf("        %s %s\n", dim("↳ 关联:"), dim(h.MatchReason))
				}
			}
			if len(it.Sources) > 0 {
				fmt.Printf("      %s %s\n", dim("来源:"), dim(strings.Join(it.Sources, "、")))
			}
			fmt.Println()
		}
	}
	if r.Message != "" && r.Status == "ok" {
		fmt.Printf("%s\n", dim(r.Message))
	}
}

// hotScoreStyle 按热度分档着色：高热红、中热黄、低热保持默认。
func hotScoreStyle(score int) string {
	s := fmt.Sprintf("%d", score)
	switch {
	case score >= 80:
		return red(s)
	case score >= 50:
		return yellow(s)
	default:
		return s
	}
}
