package ingest

import "testing"

// TestSystemIn covers what a document's own words say about its system, and
// that a document naming none is left unnamed instead of guessed.
func TestSystemIn(t *testing.T) {
	cases := []struct {
		texts []string
		want  string
	}{
		{[]string{"240515 资产库筛选改版需求文档"}, "专区与资产筛选"},
		{[]string{"【媒资管理系统】-内容管理-属性字段表"}, "媒资管理系统"},
		{[]string{"DAM-AI应用优化需求文档V1.0.0"}, "数字资产管理系统"},
		{[]string{"数字资产管理系统", "资产库列表改版", "支持按权归属筛选"}, "数字资产管理系统"},
		{[]string{"官网首页banner和行业应用管理需求文档"}, "官网运营活动"},
		{[]string{"花名册整理"}, ""},
		{[]string{}, ""},
	}
	for _, c := range cases {
		got, ok := SystemIn(c.texts...)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("SystemIn(%v) = %q, %v; want %q", c.texts, got, ok, c.want)
		}
	}
	if got, _ := SystemIn("DAM", "媒资管理系统"); got != "数字资产管理系统" {
		t.Errorf("the first text should decide, got %q", got)
	}
}

// TestSystemsVocabulary is the list the model is asked to choose from: it
// holds no duplicates and is non-empty.
func TestSystemsVocabulary(t *testing.T) {
	list := Systems()
	if len(list) < 10 {
		t.Fatalf("vocabulary is too small: %v", list)
	}
	seen := map[string]bool{}
	for _, name := range list {
		if name == "" || seen[name] {
			t.Errorf("bad vocabulary entry %q", name)
		}
		seen[name] = true
		if !SystemKnown(name) {
			t.Errorf("%q is not known", name)
		}
	}
	if SystemKnown("不存在的系统") {
		t.Error("an unknown name was reported as known")
	}
}
