package agent

import (
	"strings"
	"testing"

	"prior/ingest"
	"prior/retrieval"
	"prior/store"
)

// TestItemsTextSamplesWholeDocument covers the prompt's item budget: a long
// document's first and last requirements are both represented, not only its
// opening.
func TestItemsTextSamplesWholeDocument(t *testing.T) {
	items := make([]Item, 100)
	for i := range items {
		items[i] = Item{ID: itoa(i + 1), Quote: "需求 " + itoa(i+1)}
	}
	text := itemsText(items)
	if !strings.Contains(text, "[1]") || !strings.Contains(text, "[100]") {
		t.Fatalf("the sample lost an end of the document: %q", text)
	}
	if n := strings.Count(text, "- ["); n != itemsPromptMax {
		t.Errorf("the sample holds %d items, want %d", n, itemsPromptMax)
	}
}

// TestEvidenceRefsCarriesText covers the index the model cites from: an id
// without the text beside it is an id the model guesses, which is what made
// history evidence show a document title and no section.
func TestEvidenceRefsCarriesText(t *testing.T) {
	refs := evidenceRefs([]retrieval.DocHit{{
		Title: "会员积分规则",
		Chunks: []store.Hit{
			{ChunkID: 7, Path: []string{"4.1 积分有效期"}, Content: "积分有效期为 24 个月。"},
		},
	}})
	for _, want := range []string{"[chunk_id=7]", "会员积分规则", "4.1 积分有效期", "积分有效期为 24 个月"} {
		if !strings.Contains(refs, want) {
			t.Errorf("the citation index lost %q: %q", want, refs)
		}
	}
}

// TestRuleLayerIsDeterministic covers the half of the clarity check that
// needs no model: the patterns PRD-FINAL §8.4 fixes.
func TestRuleLayerIsDeterministic(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"3.1 积分过期"},
			Content: "积分过期规则待补充。\n用户会被自动清理。\n有效期尽快生效。"},
		{Index: 1, Path: []string{"4 流程"},
			Content: "用户提交订单后进入支付流程，支付成功后发货。"},
		{Index: 2, Path: []string{"5 枚举"},
			Content: "支持的支付方式包括微信、支付宝等渠道。"},
	}
	findings := ruleLayer(store.Doc{Title: "测试文档"}, chunks)
	bySubtype := map[string]int{}
	for _, f := range findings {
		bySubtype[f.Subtype]++
		if f.Span.Quote == "" {
			t.Errorf("%s has no span", f.Subtype)
		}
		if f.Type != "ambiguity" {
			t.Errorf("%s is not an ambiguity", f.Subtype)
		}
	}
	for _, want := range []string{"placeholder", "passive_actor", "quantified_ambiguity", "missing_branch", "incomplete_enumeration"} {
		if bySubtype[want] == 0 {
			t.Errorf("the rule layer missed %s (found %v)", want, bySubtype)
		}
	}
	if bySubtype["missing_branch"] != 1 {
		t.Errorf("a流程 without failure branches is one finding, got %d", bySubtype["missing_branch"])
	}
}

// TestEnforceKeepsTheContract covers the hard rules: every finding quotes
// the new document, and a gap or a signal without history becomes an
// ambiguity instead of a claim.
func TestEnforceKeepsTheContract(t *testing.T) {
	chunks := []store.Chunk{{Index: 0, Path: []string{"1"}, Content: "第一条原文，规则是上限 5000 元。\n第二条原文。"}}
	findings := []store.Finding{
		{Type: "logic_gap", Subtype: "conflict", Severity: "S1", Confidence: 0.9,
			Title: "冲突但没有证据", Detail: "说明", Span: store.Span{Quote: "第一条原文，规则是上限 5000 元。"}},
		{Type: "change_signal", Severity: "S0", Confidence: 0.8, Title: "变更但没有证据",
			Detail: "说明", Span: store.Span{Quote: "第二条原文"}},
		{Type: "ambiguity", Subtype: "x", Severity: "S2", Confidence: 0.3, Title: "低置信度", Detail: "说明",
			Span: store.Span{Quote: "第一条原文"}},
		{Type: "ambiguity", Subtype: "z", Severity: "S2", Confidence: 0.9, Title: "引了别的文档",
			Detail: "说明", Span: store.Span{Quote: "这是历史文档里的另一句话，本文没有。"}},
		{Type: "ambiguity", Subtype: "y", Severity: "S2", Confidence: 0.8, Title: "", Detail: "无题",
			Span: store.Span{Quote: "第二条原文"}},
	}
	out := enforce(findings, chunks)
	if len(out) != 3 {
		t.Fatalf("expected 3 findings kept (a foreign quote and an empty title dropped), got %d: %+v", len(out), out)
	}
	for _, f := range out {
		if strings.Contains(f.Title, "别的文档") {
			t.Errorf("a quote that is not in the new document survived: %s", f.Title)
		}
	}
	for _, f := range out {
		if f.Span.Quote == "" {
			t.Errorf("%s kept without a quote", f.Title)
		}
		if f.Type == "logic_gap" || f.Type == "change_signal" {
			t.Errorf("%s kept as %s without evidence", f.Title, f.Type)
		}
	}
	if out[2].Severity != "S3" {
		t.Errorf("confidence 0.3 should downgrade to S3, got %s", out[2].Severity)
	}
	if out[0].Question == "" {
		t.Error("a downgraded gap should carry a question")
	}
}

// TestChangeSuspects covers the rough筛 the model then confirms.
func TestChangeSuspects(t *testing.T) {
	chunks := []store.Chunk{
		{Content: "原有积分永久有效，本次统一改为 24 个月上限。\n新用户注册流程保持不变。"},
		{Content: "旧的支付渠道路由逻辑不再使用，全部改为直连优先。"},
	}
	suspects := changeSuspects(chunks)
	if len(suspects) != 2 {
		t.Fatalf("expected 2 change sentences, got %d: %v", len(suspects), suspects)
	}
	for _, s := range suspects {
		if !strings.Contains(s, "改为") && !strings.Contains(s, "不再") {
			t.Errorf("unexpected suspect %q", s)
		}
	}
}

// TestTermsSkippedWords covers the query terms: the words every document
// shares are not terms, and a term appears once.
func TestTermsSkippedWords(t *testing.T) {
	doc := &ingest.Doc{
		Title: "测试",
		Blocks: []ingest.Block{
			{Kind: ingest.KindParagraph, Text: "购物车结算时要校验优惠券叠加。购物车结算的校验必须同步；购物车结算还需要提示。"},
			{Kind: ingest.KindParagraph, Text: "优惠券叠加规则在购物车结算时生效，优惠券叠加的优先级要写清。"},
			{Kind: ingest.KindParagraph, Text: "需求文档的功能支持用户使用页面数据管理内容信息展示优化。"},
		},
	}
	terms := terms(doc, 10)
	if len(terms) == 0 {
		t.Fatal("no terms found")
	}
	seen := map[string]bool{}
	for _, term := range terms {
		if stopTerms[term] {
			t.Errorf("stop term %q was kept", term)
		}
		if seen[term] {
			t.Errorf("term %q repeated", term)
		}
		seen[term] = true
	}
	joined := strings.Join(terms, " ")
	if !strings.Contains(joined, "购物车") {
		t.Errorf("the repeated subject is missing from %v", terms)
	}
}
