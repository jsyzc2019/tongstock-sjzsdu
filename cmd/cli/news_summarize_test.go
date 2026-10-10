package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sjzsdu/tongstock/pkg/newsfeed"
)

func TestNewsWindowDateMode(t *testing.T) {
	now := time.Date(2026, 9, 22, 17, 30, 0, 0, time.Local)
	since, until, err := newsWindow("2026-09-21", 1, now)
	if err != nil {
		t.Fatalf("newsWindow error: %v", err)
	}
	if !since.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("since = %v", since)
	}
	if !until.Equal(time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("until = %v", until)
	}
}

func TestNewsWindowDaysMode(t *testing.T) {
	now := time.Date(2026, 9, 22, 17, 30, 0, 0, time.Local)
	since, until, err := newsWindow("", 3, now)
	if err != nil {
		t.Fatalf("newsWindow error: %v", err)
	}
	want := time.Date(2026, 9, 20, 0, 0, 0, 0, time.Local)
	if !since.Equal(want) {
		t.Fatalf("since = %v, want %v", since, want)
	}
	if !until.After(now) {
		t.Fatalf("until = %v, want after now %v", until, now)
	}

	// 默认 days=1 即当天
	since, _, err = newsWindow("", 0, now)
	if err != nil {
		t.Fatalf("newsWindow error: %v", err)
	}
	if !since.Equal(time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("default since = %v", since)
	}
}

func TestNewsWindowInvalidDate(t *testing.T) {
	if _, _, err := newsWindow("09/21/2026", 1, time.Now()); err == nil {
		t.Fatal("invalid date should error")
	}
}

func TestFilterNewsWindow(t *testing.T) {
	day := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	until := day.AddDate(0, 0, 1)
	mk := func(offset time.Duration) newsfeed.NewsSummary {
		return newsfeed.NewsSummary{Title: offset.String(), PublishTime: day.Add(offset)}
	}
	items := []newsfeed.NewsSummary{
		mk(-time.Hour),     // 窗口前，应剔除
		mk(10 * time.Hour), // 窗口内
		mk(2 * time.Hour),  // 窗口内
		mk(25 * time.Hour), // 窗口后（次日），应剔除
	}
	got := filterNewsWindow(items, day, until)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if !got[0].PublishTime.After(got[1].PublishTime) {
		t.Fatalf("not sorted desc: %v", got)
	}
}

func TestBuildNewsBriefingPrompt(t *testing.T) {
	since := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)
	until := since.AddDate(0, 0, 1)
	res := &newsfeed.StockNewsResult{
		Code: "001216",
		Items: []newsfeed.NewsSummary{
			{
				Source:      newsfeed.SourceCaiLianShe,
				NewsType:    newsfeed.NewsTypeFlash,
				Title:       "华瓷股份6连板",
				Summary:     "MLCC概念股华瓷股份收获6连板。",
				PublishTime: since.Add(10 * time.Hour),
			},
		},
		Degraded: []newsfeed.SourceDegradation{{Source: "证券时报", Error: "解析失败"}},
	}
	prompt := buildNewsBriefingPrompt("001216", since, until, res)
	for _, want := range []string{
		"生成一份中文简报",
		"股票代码：001216",
		"资讯条数：1 条（渠道：财联社）",
		"数据源降级：证券时报（解析失败）",
		"华瓷股份6连板",
		"摘要：MLCC概念股华瓷股份收获6连板。",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
