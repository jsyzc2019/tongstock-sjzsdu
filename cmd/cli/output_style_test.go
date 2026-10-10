package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sjzsdu/tongstock/pkg/newsfeed"
)

func TestPaintRespectsColorEnabled(t *testing.T) {
	orig := colorEnabled
	t.Cleanup(func() { colorEnabled = orig })

	colorEnabled = false
	if got := red("x"); got != "x" {
		t.Fatalf("red() with color disabled = %q, want plain %q", got, "x")
	}

	colorEnabled = true
	got := red("x")
	if !strings.Contains(got, "\x1b[31m") || !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("red() with color enabled = %q, want ANSI-wrapped", got)
	}
}

func TestChangeStyle(t *testing.T) {
	orig := colorEnabled
	t.Cleanup(func() { colorEnabled = orig })
	colorEnabled = true

	tests := []struct {
		name string
		v    float64
		want string // 空串表示不着色
	}{
		{name: "涨为红", v: 1.5, want: "\x1b[31m"},
		{name: "跌为绿", v: -1.5, want: "\x1b[32m"},
		{name: "平盘不着色", v: 0, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := changeStyle(tt.v, "1.00")
			if tt.want == "" {
				if strings.Contains(got, "\x1b") {
					t.Fatalf("changeStyle(%v) = %q, want no color", tt.v, got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("changeStyle(%v) = %q, want prefix %q", tt.v, got, tt.want)
			}
		})
	}
}

func TestHotScoreStyle(t *testing.T) {
	orig := colorEnabled
	t.Cleanup(func() { colorEnabled = orig })
	colorEnabled = true

	tests := []struct {
		name  string
		score int
		want  string // 空串表示不着色
	}{
		{name: "高热为红", score: 90, want: "\x1b[31m"},
		{name: "中热为黄", score: 60, want: "\x1b[33m"},
		{name: "低热不着色", score: 10, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hotScoreStyle(tt.score)
			if tt.want == "" {
				if strings.Contains(got, "\x1b") {
					t.Fatalf("hotScoreStyle(%d) = %q, want no color", tt.score, got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("hotScoreStyle(%d) = %q, want prefix %q", tt.score, got, tt.want)
			}
		})
	}
}

func TestPrintTopicsPlainWhenColorDisabled(t *testing.T) {
	origColor, origWide := colorEnabled, topicWide
	t.Cleanup(func() {
		colorEnabled, topicWide = origColor, origWide
	})
	colorEnabled = false
	topicWide = true

	r := &newsfeed.HotTopicResult{
		Date:        "2026-10-09",
		TradingDate: "2026-10-09",
		Status:      "ok",
		Items: []newsfeed.HotTopicStock{
			{
				Code: "600519", Name: "贵州茅台", Mentions: 5, HotScore: 88,
				Sources: []string{"财联社"},
				Headlines: []newsfeed.HotTopicHeadline{{
					Title:       "贵州茅台三季度业绩预告",
					Source:      "财联社",
					PublishTime: time.Date(2026, 10, 9, 9, 30, 0, 0, time.Local),
					MatchReason: "标题命中",
				}},
			},
		},
	}

	out := captureStdout(t, func() { printTopics(r) })

	if strings.Contains(out, "\x1b") {
		t.Fatalf("color disabled but output has ANSI escapes:\n%q", out)
	}
	for _, want := range []string{
		"热门股票", "600519", "贵州茅台", "提及 5 次", "热度 88",
		"贵州茅台三季度业绩预告", "标题命中", "来源:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintTopicsColorizedOnTTY(t *testing.T) {
	origColor, origWide := colorEnabled, topicWide
	t.Cleanup(func() {
		colorEnabled, topicWide = origColor, origWide
	})
	colorEnabled = true
	topicWide = false

	r := &newsfeed.HotTopicResult{
		Date:        "2026-10-09",
		TradingDate: "2026-10-09",
		Status:      "stale",
		Items: []newsfeed.HotTopicStock{
			{Code: "000001", Name: "平安银行", Mentions: 2, HotScore: 90},
			{Code: "000002", Name: "万科A", Mentions: 1, HotScore: 30},
		},
	}

	out := captureStdout(t, func() { printTopics(r) })

	for _, want := range []struct{ name, sgr string }{
		{"标题加粗", "\x1b[1m"},
		{"代码青色", "\x1b[36m"},
		{"stale 提示黄色", "\x1b[33m"},
		{"高热红色", "\x1b[31m"},
	} {
		if !strings.Contains(out, want.sgr) {
			t.Fatalf("%s: output missing SGR %s:\n%q", want.name, want.sgr, out)
		}
	}
}

// captureStdout 捕获 fn 写到 os.Stdout 的输出。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	w.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	r.Close()
	return buf.String()
}
