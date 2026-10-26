package agent

import (
	"strings"
	"testing"

	"prior/store"
)

// TestRuleLayerScopeAndReference covers the two vague forms a reviewer sees
// most often in Chinese requirement documents: a scope word with no list
// ("所有的都不要") and a time or version reference with no anchor
// ("之前的不要"). A sentence that names what it means stays unflagged.
func TestRuleLayerScopeAndReference(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"二、改动"}, Content: "所有的都不要。\n之前的不要。\n之前的需求都不要。\n所有用户都能看到会员价。"},
	}
	findings := ruleLayer(store.Doc{Title: "测试文档"}, chunks)
	bySubtype := map[string]int{}
	for _, f := range findings {
		bySubtype[f.Subtype]++
		if f.Type != "ambiguity" {
			t.Errorf("%s is not an ambiguity", f.Subtype)
		}
		if strings.TrimSpace(f.Question) == "" || strings.TrimSpace(f.Detail) == "" {
			t.Errorf("%s does not say why or what to ask", f.Subtype)
		}
	}
	if bySubtype["open_scope"] != 1 {
		t.Errorf("open scope findings = %d, want 1 (all: %v)", bySubtype["open_scope"], bySubtype)
	}
	if bySubtype["unresolved_reference"] != 2 {
		t.Errorf("unresolved reference findings = %d, want 2 (all: %v)", bySubtype["unresolved_reference"], bySubtype)
	}
	if bySubtype["open_scope"]+bySubtype["unresolved_reference"] != 3 {
		t.Errorf("a scoped sentence was flagged as well: %v", bySubtype)
	}
}

// TestOneLinerRuleFindsShortRequirements covers the requirement S0 is most
// likely to skip: one short line, no trigger, no acceptance. A line with a
// number or an explicit non-goal is not one.
func TestOneLinerRuleFindsShortRequirements(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"四、功能"}, Content: "支持导出。\n列表增加筛选。\n点击后跳转详情。\n" +
			"列表在 10 万条数据下 P99 延迟不超过 200ms。\n本次不修改登录流程。\n产品设计图"},
	}
	findings := oneLinerFindings(chunks)
	if len(findings) != 3 {
		t.Fatalf("one-liners = %d (%v), want 3", len(findings), quotesOf(findings))
	}
	for _, f := range findings {
		if f.Type != "one_liner" || f.Subtype != "one_sentence" {
			t.Errorf("unexpected type %s/%s", f.Type, f.Subtype)
		}
		if !strings.HasPrefix(f.Title, "一句话需求：") {
			t.Errorf("title reads %q", f.Title)
		}
		if strings.TrimSpace(f.Span.Quote) == "" || len(f.Span.Path) == 0 {
			t.Errorf("finding has no quote or section: %+v", f)
		}
	}
	for _, f := range findings {
		if strings.Contains(f.Span.Quote, "P99") || strings.Contains(f.Span.Quote, "不修改") {
			t.Errorf("a quantified or non-goal line was flagged: %q", f.Span.Quote)
		}
	}
}

// TestDedupeFindings covers the same conclusion reached by the model and the
// rule layer: one finding, the more confident one, while two conflicts on the
// same new sentence stay separate because their history differs.
func TestDedupeFindings(t *testing.T) {
	findings := []store.Finding{
		{Type: "one_liner", Confidence: 0.6, Detail: "short", Span: store.Span{Quote: "支持导出"}},
		{Type: "one_liner", Confidence: 0.9, Detail: "longer detail", Span: store.Span{Quote: "支持导出。"}},
		{Type: "logic_gap", Confidence: 0.8, Span: store.Span{Quote: "退款规则"},
			Evidence: []store.Evidence{{Quote: "退款 7 天"}}},
		{Type: "logic_gap", Confidence: 0.8, Span: store.Span{Quote: "退款规则"},
			Evidence: []store.Evidence{{Quote: "退款 3 天"}}},
	}
	out := dedupeFindings(findings)
	if len(out) != 3 {
		t.Fatalf("deduped to %d findings (%v), want 3", len(out), quotesOf(out))
	}
	if out[0].Confidence != 0.9 {
		t.Errorf("kept the less confident duplicate: %+v", out[0])
	}
}

func quotesOf(findings []store.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Span.Quote)
	}
	return out
}
