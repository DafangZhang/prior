package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"prior/store"
)

// TestMarksFindQuotesAcrossWhitespace covers the document view's marks: the
// model quotes the document's words, and the whitespace between them differs
// — a newline where the chunk had none, a full-width space — which is not a
// different quote. A quote that is not in the document is not marked.
func TestMarksFindQuotesAcrossWhitespace(t *testing.T) {
	text := "一、规则\n退款在 7 个工作日内原路退回。\n超过时限的申请需要运营主管审批。"
	findings := []finding{
		{ID: "1", Severity: "S2", Title: "时限", Detail: "d", NewQuote: "退款在 7 个工作日内原路退回"},
		{ID: "2", Severity: "S1", Title: "审批", Detail: "d", NewQuote: "超过时限的申请\n需要运营主管审批"},
		{ID: "3", Severity: "S2", Title: "不在文中", Detail: "d", NewQuote: "这条不在文档里"},
	}
	marks := marksIn(text, findings)
	if len(marks) != 2 {
		t.Fatalf("marked %d quotes, want the two that are in the document: %+v", len(marks), marks)
	}
	if got := text[marks[0].start:marks[0].end]; got != "退款在 7 个工作日内原路退回" {
		t.Errorf("first mark reads %q", got)
	}
	// A quote written with a newline is marked where the document has it as
	// one run of text.
	if got := text[marks[1].start:marks[1].end]; !strings.Contains(got, "超过时限的申请") || !strings.Contains(got, "运营主管审批") {
		t.Errorf("second mark reads %q", got)
	}
	// Marks come in document order and do not overlap.
	for i := 1; i < len(marks); i++ {
		if marks[i].start < marks[i-1].end {
			t.Errorf("mark %d overlaps the one before it", i)
		}
	}
}

// TestMarksSplitAcrossChunks covers a quote that runs past the chunk it
// starts in: each chunk draws its own part of the sentence, and the text is
// not repeated inside the first paragraph.
func TestMarksSplitAcrossChunks(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Content: "请求带 Authorization: Bearer <token> 头"},
		{Index: 1, Content: "并在会话过期后跳转登录页。"},
	}
	text, spans := layOut(chunks)
	if len(spans) != 2 {
		t.Fatalf("laid out %d chunks", len(spans))
	}
	findings := []finding{{ID: "1", Severity: "S2", Title: "认证", Detail: "d",
		NewQuote: "请求带 Authorization: Bearer <token> 头\n并在会话过期后跳转登录页。"}}
	marks := marksIn(text, findings)
	if len(marks) != 1 {
		t.Fatalf("marked %d quotes", len(marks))
	}
	first := clip(marks, spans[0])
	if len(first) != 1 {
		t.Fatalf("the first chunk draws %d marks", len(first))
	}
	if first[0].end != spans[0].End {
		t.Errorf("the first part ends at %d, want the chunk's end %d", first[0].end, spans[0].End)
	}
	second := clip(marks, spans[1])
	if len(second) != 1 {
		t.Fatalf("the second chunk draws %d marks, want the rest of the quote", len(second))
	}
	if second[0].start != spans[1].Start || second[0].end != spans[1].End {
		t.Errorf("the second part reads [%d,%d], want the whole second chunk", second[0].start, second[0].end)
	}
}

// TestLayOutCleansOldMarkup covers a chunk written before the parser
// stripped the editor's HTML: the report shows the sentence, not the tags.
func TestLayOutCleansOldMarkup(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"一、版本"}, Content: `<span style="background-color: rgb(248, 249, 250);">版本号:V1.0.0</span><br>审核人：林知远`},
	}
	text, spans := layOut(chunks)
	for _, leaked := range []string{"<span", "style=", "<br>"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the document view still holds %q: %q", leaked, text)
		}
	}
	if !strings.Contains(text, "版本号:V1.0.0") || !strings.Contains(text, "审核人:林知远") {
		t.Errorf("the text was lost: %q", text)
	}
	if got := text[spans[0].Start:spans[0].End]; got != text {
		t.Errorf("the span reads %q", got)
	}
}

// TestChunkHeadingIsNotDrawnTwice covers the section title a chunk repeats
// at the start of its body: the heading path above it is the same title, and
// showing both reads as a duplicated section.
func TestChunkHeadingIsNotDrawnTwice(t *testing.T) {
	a := newApp(defaultSettings())
	text := "一、版本信息\n版本号 V1.0.0"
	span := chunkSpan{
		Chunk: store.Chunk{Index: 0, Path: []string{"一、版本信息"}},
		Start: 0, End: len(text),
	}
	view := func(c *ui.Context) {
		a.chunkParagraph(c, paletteFor(&ui.Theme{}, sidebarColors[0]), text, span, nil)
	}
	tester := ui.NewTester(view, 800, 400)
	tester.Frame()
	count := strings.Count(strings.Join(tester.Texts(), "\n"), "一、版本信息")
	if count != 1 {
		t.Fatalf("the section title appears %d times, want once: %v", count, tester.Texts())
	}
}

// TestLayOutKeepsOffsets covers the document as one text: each chunk's span
// points at its own words, so a quote found in the whole text can be drawn in
// the chunk it belongs to.
func TestLayOutKeepsOffsets(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Content: "第一段"},
		{Index: 1, Content: "第二段"},
	}
	text, spans := layOut(chunks)
	for i, span := range spans {
		if got := text[span.Start:span.End]; got != chunks[i].Content {
			t.Errorf("chunk %d's span reads %q, want %q", i, got, chunks[i].Content)
		}
	}
	if spans[1].Start <= spans[0].End {
		t.Error("the chunks do not follow one another")
	}
}

// TestOverlayKeepsPageShortcutsStill covers the report dialog over the
// library: Cmd+F searches the report in front, not the page behind it, and
// moving to another page puts the report away.
func TestOverlayKeepsPageShortcutsStill(t *testing.T) {
	libSearching, reportSearching = false, false
	a := newTestApp(t)
	a.router.Push("/report/1")
	tester := ui.NewTester(a.view, 1024, 900)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyF)
	tester.Frame()
	if !reportSearching {
		t.Fatal("Cmd+F did not open the report's search")
	}
	if libSearching {
		t.Fatal("Cmd+F opened the library's search behind the report")
	}
	reportSearching = false

	a.router.Push("/library")
	tester.Frame()
	if a.reportOpen {
		t.Fatal("moving to another page left the report open over it")
	}
}

// TestFindingWithoutHistoryDrawsOneColumn covers the card of an ambiguity
// that has no historical quote: the evidence row keeps the new document's
// sentence and does not draw an empty "earlier document" column.
func TestFindingWithoutHistoryDrawsOneColumn(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/report/1")
	tester := ui.NewTester(a.view, 1024, 900)
	tester.Frame()
	a.findings = []finding{{
		ID: "1", Type: "ambiguity", Severity: "S2", Title: "这条没有历史对照",
		Detail: "d", NewQuote: "支持导出", NewPath: "二、需求说明",
	}}
	reportMode, reportTab, reportCurrent = modeFindings, -1, 0
	tester.Frame()
	if !tester.HasText("这条没有历史对照") {
		t.Fatalf("the finding card is not showing: %v", tester.Texts())
	}
	if tester.HasText("历史文档") {
		t.Fatalf("an empty earlier-document column is still drawn: %v", tester.Texts())
	}
}

// TestClosingReportLeavesTheList covers Escape on a report opened by route:
// the route is replaced by the list, and the next frame does not open the
// same report again.
func TestClosingReportLeavesTheList(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/report/1")
	tester := ui.NewTester(a.view, 1024, 900)
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if a.reportOpen {
		t.Fatal("Escape did not close the report")
	}
	if got := a.router.Path(); got != "/reports" {
		t.Fatalf("closing left the route at %q, want /reports", got)
	}
	if !tester.HasText("订单履约 × 支付渠道路由 对比分析") {
		t.Fatal("the report list is not showing after close")
	}
}

// TestMarkClickOpensItsFinding covers a mark's click: it keeps the document
// in view and opens the finding beside it, instead of replacing the document
// with another page.
func TestMarkClickOpensItsFinding(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/report/1")
	tester := ui.NewTester(a.view, 1024, 900)
	tester.Frame()
	a.findings = []finding{
		{ID: "10", Type: "ambiguity", Severity: "S2", Title: "不明确 A", Detail: "d", NewQuote: "甲"},
		{ID: "11", Type: "logic_gap", Severity: "S1", Title: "逻辑漏洞 B", Detail: "d", NewQuote: "乙"},
	}
	reportMode = modeDocument
	a.selectFinding(a.findings[1])
	tester.Frame()
	if reportMode != modeDocument {
		t.Fatalf("selecting a finding changed the view to %q", reportMode)
	}
	if reportCurrent != 0 {
		t.Fatalf("the logic gap sits at %d in the list, want 0 (S1 first)", reportCurrent)
	}
	if !tester.HasText("逻辑漏洞 B") {
		t.Fatalf("the finding's detail is not showing: %v", tester.Texts())
	}
}

// TestFindingIndexFollowsTheTab covers the index a mark uses: it is the
// finding's place in the tab's own list, not in the run's whole list.
func TestFindingIndexFollowsTheTab(t *testing.T) {
	a := newTestApp(t)
	a.findings = []finding{
		{ID: "1", Type: "ambiguity", Title: "a"},
		{ID: "2", Type: "logic_gap", Title: "b"},
		{ID: "3", Type: "ambiguity", Title: "c"},
	}
	reportTab = findingTabOf("logic_gap")
	if got := a.findingIndex("2"); got != 0 {
		t.Fatalf("the logic gap's index is %d, want 0 in its own tab", got)
	}
	reportTab = findingTabOf("ambiguity")
	if got := a.findingIndex("3"); got != 1 {
		t.Fatalf("the second ambiguity's index is %d, want 1", got)
	}
	if got := a.findingIndex("2"); got != -1 {
		t.Fatalf("a finding of another tab is at %d, want -1", got)
	}
}

// TestReportKeepsItsViews covers the report's menu: the document, the
// findings and the ledger are three views of one run, and the menu keeps the
// same entries whichever one is showing.
func TestReportKeepsItsViews(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/report/1")
	tester := ui.NewTester(a.view, 1100, 900)
	tester.Frame()
	for _, mode := range []string{modeDocument, modeFindings, modeLedger, modeVerdict} {
		reportMode = mode
		reportCurrent = 0
		tester.Frame()
		for _, entry := range []string{"文档", "发现", "变更账本", "模块需求历史"} {
			if !tester.HasText(entry) {
				t.Fatalf("%s shows no %q menu entry", mode, entry)
			}
		}
	}
	reportMode = modeDocument
}
