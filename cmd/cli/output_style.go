package main

import "os"

// 终端输出着色。仅在 stdout 是交互式终端时启用，且遵循 NO_COLOR /
// CLICOLOR=0 约定，保证管道、重定向与脚本消费时输出是纯文本。
var colorEnabled = detectColor(os.Stdout)

func detectColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("CLICOLOR") == "0" {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// paint 给 s 加上 ANSI 色码 sgr；colorEnabled 为假时原样返回，
// 以便非终端输出不携带转义序列。
func paint(sgr, s string) string {
	if !colorEnabled {
		return s
	}
	return "\x1b[" + sgr + "m" + s + "\x1b[0m"
}

func bold(s string) string   { return paint("1", s) }
func dim(s string) string    { return paint("2", s) }
func red(s string) string    { return paint("31", s) }
func green(s string) string  { return paint("32", s) }
func yellow(s string) string { return paint("33", s) }
func blue(s string) string   { return paint("34", s) }
func cyan(s string) string   { return paint("36", s) }

// changeStyle 按 A 股习惯给涨跌数值着色：红涨、绿跌、平不变。
func changeStyle(v float64, s string) string {
	switch {
	case v > 0:
		return red(s)
	case v < 0:
		return green(s)
	default:
		return s
	}
}
