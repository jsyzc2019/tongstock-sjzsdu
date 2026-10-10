package newsfeed

import "testing"

func testEntities() []StockEntity {
	return []StockEntity{
		{Code: "600519", Name: "贵州茅台"},
		{Code: "000001", Name: "平安银行"},
		{Code: "601318", Name: "中国平安"},
		{Code: "000056", Name: "*ST皇庭"},
		{Code: "600036", Name: "招商银行"},
		{Code: "601872", Name: "招商轮船"},
	}
}

func TestEntityMatcherCodeHit(t *testing.T) {
	m := NewEntityMatcher(testEntities())
	refs := m.Match("贵州茅台(600519)今日涨1%，平安银行(000001)跟涨")
	byCode := map[string]StockRef{}
	for _, r := range refs {
		byCode[r.Code] = r
	}
	if got := byCode["600519"]; got.MatchType != MatchCodeHit || got.Confidence != 1.0 {
		t.Fatalf("600519 = %+v", got)
	}
	if got := byCode["000001"]; got.MatchType != MatchCodeHit {
		t.Fatalf("000001 = %+v", got)
	}
}

// 东财返回的高亮标签会把简称切断，必须仍能命中。
func TestEntityMatcherStripsHighlightTags(t *testing.T) {
	m := NewEntityMatcher(testEntities())
	refs := m.Match("其次是源杰科技、<em>贵州茅台</em>等，最新收盘价分别为1701.00元")
	if len(refs) == 0 {
		t.Fatal("高亮标签导致简称匹配失效")
	}
	if refs[0].Code != "600519" || refs[0].MatchType != MatchNameHit {
		t.Fatalf("got %+v", refs[0])
	}
	if refs[0].Confidence < 0.9 {
		t.Fatalf("4 字简称置信度过低: %v", refs[0].Confidence)
	}
}

// 最长匹配优先：包含「招商银行」时不应被更短的「招商」类短名抢走。
func TestEntityMatcherLongestFirst(t *testing.T) {
	m := NewEntityMatcher(testEntities())
	refs := m.Match("招商银行与招商轮船双双上涨")
	codes := map[string]bool{}
	for _, r := range refs {
		codes[r.Code] = true
	}
	if !codes["600036"] || !codes["601872"] {
		t.Fatalf("应同时命中两只股票, got %v", codes)
	}
}

func TestEntityMatcherHandlesSTPrefix(t *testing.T) {
	m := NewEntityMatcher(testEntities())
	refs := m.Match("*ST皇庭发布退市风险提示，皇庭国际回应")
	if len(refs) == 0 || refs[0].Code != "000056" {
		t.Fatalf("got %+v", refs)
	}
	// 弱别名「皇庭」不得把全称命中的置信度拉低。
	if refs[0].Confidence < 0.9 {
		t.Fatalf("全称命中被短别名降级: %+v", refs[0])
	}
}

// ST 前缀剥离出的 2 字简称（*ST发展→发展、ST标准→标准）是超高频通用词，
// 必须低于默认阈值 0.5：否则「服务业发展」「水利行业标准」这类标题会把
// 与之毫无关系的 ST 股推上热门榜。
func TestEntityMatcherTwoCharAliasIsWeak(t *testing.T) {
	m := NewEntityMatcher([]StockEntity{
		{Code: "000838", Name: "*ST发展"},
		{Code: "600302", Name: "ST标准"},
	})
	titles := []string{
		"人社部：将研究出台服务业发展促就业文件 扩大服务业就业总量",
		"我国将开展适应人工智能发展促就业行动",
		"《团体标准化 良好行为规范》国标发布",
		"水利部发布8项水利行业标准",
	}
	for _, title := range titles {
		for _, r := range m.Match(title) {
			if r.Confidence >= DefaultMinConfidence {
				t.Fatalf("%q 不应产生强关联: %+v", title, r)
			}
		}
	}
}

// 完整 ST 简称（含/不含星号）仍是强关联：真实报道不能因弱化通用词而丢失。
func TestEntityMatcherSTFullNameStillStrong(t *testing.T) {
	m := NewEntityMatcher([]StockEntity{{Code: "000838", Name: "*ST发展"}})
	for _, tc := range []struct {
		title string
		min   float64
	}{
		{"*ST发展发布退市风险提示公告", 0.9},
		{"ST发展披露整改进展", 0.75},
	} {
		refs := m.Match(tc.title)
		if len(refs) == 0 || refs[0].Code != "000838" {
			t.Fatalf("%q 未命中: %+v", tc.title, refs)
		}
		if refs[0].Confidence < tc.min {
			t.Fatalf("%q 置信度 %v 低于 %v", tc.title, refs[0].Confidence, tc.min)
		}
	}
}

func TestEntityMatcherNoEntitiesNeverGuesses(t *testing.T) {
	m := NewEntityMatcher(nil)
	if !m.Empty() {
		t.Fatal("空名录应标记为空")
	}
	if refs := m.Match("贵州茅台(600519)大涨"); len(refs) != 0 {
		t.Fatalf("无名录时不应产生任何关联: %+v", refs)
	}
}

func TestEntityMatcherIgnoresUnknownCode(t *testing.T) {
	m := NewEntityMatcher(testEntities())
	refs := m.Match("订单编号 123456 已生成")
	if len(refs) != 0 {
		t.Fatalf("非股票代码不应命中: %+v", refs)
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		"<em>茅台</em>":                   "茅台",
		"<p>  a  b </p>":                "a b",
		"a&nbsp;b":                      "a b",
		"<span class=\"x\">中国平安</span>": "中国平安",
	}
	for in, want := range cases {
		if got := StripHTML(in); got != want {
			t.Fatalf("StripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}
