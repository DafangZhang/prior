package agent

import (
	"strings"
	"testing"

	"prior/store"
)

// TestRuleLayerVagueClasses covers the skill's three vague classes: a UI
// wording with no state, a performance wording with no number, and a range
// left open. Each has to come back with what was said, why it cannot be
// built, and what to ask.
func TestRuleLayerVagueClasses(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"三、交互"}, Content: "上传失败时给出友好提示。"},
		{Index: 1, Path: []string{"四、性能"}, Content: "列表需要支持海量数据，并做到实时刷新。"},
		{Index: 2, Path: []string{"五、范围"}, Content: "本期支持视频、音频、图片等等。"},
	}
	doc := store.Doc{Title: "测试文档"}
	findings := ruleLayer(doc, chunks)

	bySubtype := map[string]store.Finding{}
	for _, f := range findings {
		bySubtype[f.Subtype] = f
		if strings.TrimSpace(f.Span.Quote) == "" {
			t.Errorf("%s has no quote", f.Subtype)
		}
		if strings.TrimSpace(f.Detail) == "" || strings.TrimSpace(f.Question) == "" {
			t.Errorf("%s does not say why or what to ask: %+v", f.Subtype, f)
		}
		if f.Type != "ambiguity" {
			t.Errorf("%s is not filed as an ambiguity", f.Subtype)
		}
	}
	for _, want := range []string{"unquantified_ui", "unquantified_performance", "open_enumeration"} {
		if _, ok := bySubtype[want]; !ok {
			t.Errorf("missed %s (found %v)", want, subtypeKeys(bySubtype))
		}
	}
	// The performance finding names the words it read, and the question asks
	// for a number.
	perf := bySubtype["unquantified_performance"]
	if !strings.Contains(perf.Detail, "海量") || !strings.Contains(perf.Question, "P99") {
		t.Errorf("the performance finding reads %q / %q", perf.Detail, perf.Question)
	}
	// A document that states its numbers is not asked for them.
	clear := ruleLayer(doc, []store.Chunk{
		{Index: 0, Content: "列表在 10 万条数据下 P99 延迟不超过 200ms，数据变更后 5 秒内刷新。"},
	})
	for _, f := range clear {
		if f.Subtype == "unquantified_performance" {
			t.Errorf("a quantified requirement was flagged: %q", f.Span.Quote)
		}
	}
}

func subtypeKeys(m map[string]store.Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
