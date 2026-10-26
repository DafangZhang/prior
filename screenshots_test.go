package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"prior/store"
)

// TestScreenshots renders the demo data the README shows. It is skipped in
// the normal test run and writes into docs/screenshots when asked:
//
//	PRIOR_SHOTS=1 go test . -run TestScreenshots -count=1
//
// The images are the built-in sample, not a real knowledge base: the README
// labels them as such.
func TestScreenshots(t *testing.T) {
	if os.Getenv("PRIOR_SHOTS") == "" {
		t.Skip("set PRIOR_SHOTS=1 to write docs/screenshots")
	}
	dir := filepath.Join("docs", "screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() {
		libSelected, libFilter, libSearchQuery = -1, "all", ""
		reportMode, reportTab, reportCurrent = modeDocument, -1, -1
		verdictQuestionsOpen = false
	}()

	// The list: six reviews with different states and verdicts.
	list := demoApp(t)
	list.store = &store.Store{}
	list.env.pgVector = true
	list.router.Push("/reports")
	renderShot(t, list, filepath.Join(dir, "reports.png"), false)

	// The report dialog: a document with a selected finding beside it.
	doc := demoApp(t)
	openDemoReport(t, doc)
	doc.reportSelected = "c1"
	renderShot(t, doc, filepath.Join(dir, "report-document.png"), false)

	// The findings list, including one decided and one waiting.
	findings := demoApp(t)
	openDemoReport(t, findings)
	reportMode, reportTab = modeFindings, -1
	renderShot(t, findings, filepath.Join(dir, "report-findings.png"), false)

	// The pre-review: verdict, risks, and the question list.
	verdict := demoApp(t)
	openDemoReport(t, verdict)
	reportMode = modeVerdict
	verdictQuestionsOpen = true
	renderShot(t, verdict, filepath.Join(dir, "report-verdict.png"), false)
	verdictQuestionsOpen = false

	// The same document in the dark appearance.
	dark := demoApp(t)
	openDemoReport(t, dark)
	dark.reportSelected = "g1"
	renderShot(t, dark, filepath.Join(dir, "report-dark.png"), true)

	// The library, with its inspector and chunk list.
	library := demoApp(t)
	library.store = &store.Store{}
	library.currentDoc = 1
	library.currentChunks = demoChunks()
	libSelected = 1
	renderShot(t, library, filepath.Join(dir, "library.png"), false)
}

// demoApp is the sample data the screenshots use: the same fixture documents,
// with the runs named after the documents they reviewed.
func demoApp(t *testing.T) *app {
	t.Helper()
	a := newTestApp(t)
	a.runs = []run{
		{ID: 4, Title: "优惠券叠加规则 v0.9 · 评审", Status: "succeeded", Stage: "done",
			Findings: [4]int{0, 1, 3, 2}, Counts: map[string]int{
				"change_signal": 1, "logic_gap": 2, "ambiguity": 2, "one_liner": 1,
			}, Total: 6, Pending: 1, Verdict: "clarify",
			StartedAt: "2025-10-14 10:31", Elapsed: "3m 04s", DocsHit: 3, DocID: 4},
		{ID: 1, Title: "会员积分规则 PRD v2.1 · 评审", Status: "succeeded", Stage: "done",
			Findings: [4]int{0, 3, 2, 0}, Counts: map[string]int{
				"change_signal": 1, "logic_gap": 1, "ambiguity": 1, "one_liner": 1, "pre_review": 1,
			}, Total: 5, Pending: 3, Verdict: "clarify",
			StartedAt: "2025-10-12 09:42", Elapsed: "3m 51s", DocsHit: 2, DocID: 1},
		{ID: 2, Title: "订单履约流程 v1.4 · 评审", Status: "failed", Stage: "retrieval",
			Findings: [4]int{}, Counts: map[string]int{}, StartedAt: "2025-10-10 16:08",
			Elapsed: "0m 47s", DocsHit: 1, DocID: 2},
		{ID: 5, Title: "消息推送中心 v2.2 · 评审", Status: "degraded", Stage: "S4",
			Findings: [4]int{0, 0, 3, 1}, Counts: map[string]int{
				"ambiguity": 2, "one_liner": 2,
			}, Total: 4, Pending: 4,
			StartedAt: "2025-10-08 14:26", Elapsed: "2m 36s", DocsHit: 1, DocID: 5},
		{ID: 3, Title: "支付渠道路由方案 v3.0 · 评审", Status: "succeeded", Stage: "done",
			Findings: [4]int{0, 1, 4, 3}, Counts: map[string]int{
				"change_signal": 2, "logic_gap": 3, "ambiguity": 2, "one_liner": 1,
			}, Total: 8, Pending: 2, Verdict: "ready",
			StartedAt: "2025-09-28 11:20", Elapsed: "4m 12s", DocsHit: 3, DocID: 3},
		{ID: 6, Title: "风控名单同步方案 v1.1 · 评审", Status: "failed", Stage: "parse",
			Findings: [4]int{}, Counts: map[string]int{}, StartedAt: "2025-09-25 16:58",
			Elapsed: "0m 12s", DocsHit: 0, DocID: 6},
	}
	a.pendingAll = 10
	// The inspector shows six demo chunks, so the document that holds them
	// says six as well.
	for i := range a.docs {
		if a.docs[i].ID == 1 {
			a.docs[i].Chunks, a.docs[i].Chars = 6, 520
		}
	}
	return a
}

// openDemoReport opens run 1 and replaces what the sample loads with the
// demo document, findings and verdict the screenshots show.
func openDemoReport(t *testing.T, a *app) {
	t.Helper()
	a.router.Push("/report/1")
	tester := ui.NewTester(a.view, 1440, 900)
	tester.Frame()
	a.store = &store.Store{}
	a.reviewDocID = 1
	a.reviewChunks = demoChunks()
	a.reviewText, a.reviewSpans = "", nil
	a.findings = demoFindings()
	a.preReview, a.hasPreReview = demoPreReview(), true
	a.ledger = demoLedger()
	a.reportLoaded = true
}

func renderShot(t *testing.T, a *app, path string, dark bool) {
	t.Helper()
	tester := ui.NewTester(a.view, 1440, 900)
	tester.SetScale(1)
	tester.SetDark(dark)
	tester.Frame()
	tester.Frame()
	img := tester.Image()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%dx%d)", path, img.Bounds().Dx(), img.Bounds().Dy())
}

func demoChunks() []store.Chunk {
	return []store.Chunk{
		{ID: 1, DocID: 1, Index: 0, Path: []string{"一、版本信息"}, Tokens: 24, Content: "一、版本信息\n" +
			"版本号:V2.1.0\n审核人:林知远 创建日期:2025-10-12"},
		{ID: 2, DocID: 1, Index: 1, Path: []string{"二、积分规则"}, Tokens: 68, Content: "二、积分规则\n" +
			"签到积分与消费积分合并计算。积分有效期为 24 个月,到期自动清零。支持导出。"},
		{ID: 3, DocID: 1, Index: 2, Path: []string{"三、存量处理"}, Tokens: 42, Content: "三、存量处理\n" +
			"历史积分同样长期有效。之前的活动规则不要。"},
		{ID: 4, DocID: 1, Index: 3, Path: []string{"四、积分兑换"}, Tokens: 55, Content: "四、积分兑换\n" +
			"100 积分可兑换 1 元商品。兑换比例与库存由运营在后台配置，换完即止。"},
		{ID: 5, DocID: 1, Index: 4, Path: []string{"五、异常与回滚"}, Tokens: 48, Content: "五、异常与回滚\n" +
			"支付失败时不扣减积分；重复提交按幂等处理，同一订单只记一次积分。"},
		{ID: 6, DocID: 1, Index: 5, Path: []string{"六、验收标准"}, Tokens: 60, Content: "六、验收标准\n" +
			"积分明细导出在 10 万条数据下 P99 不超过 2 秒；过期任务每日 02:00 执行，失败自动重试 3 次。"},
	}
}

func demoFindings() []finding {
	return []finding{
		{ID: "c1", Type: "change_signal", Subtype: "modified_threshold", Severity: "S1", Confidence: 0.91,
			Title:    "积分有效期由永久改为 24 个月，但未交代存量积分",
			Detail:   "新规则把有效期设为 24 个月，但没有说明已产生的积分如何处置；下游积分计算与过期任务需要明确。",
			NewQuote: "积分有效期为 24 个月,到期自动清零。", NewPath: "二、积分规则",
			OldQuote: "积分永久有效,不设过期机制。", OldPath: "3.2 积分过期规则",
			OldDocTitle: "会员积分规则 PRD v1.7",
			Question:    "存量积分按新规则重算，还是只对新增积分生效？",
			Impact:      "影响积分余额、过期任务与对账。", Status: "open"},
		{ID: "g1", Type: "logic_gap", Subtype: "internal_conflict", Severity: "S1", Confidence: 0.86,
			Title:    "积分有效期在文档内前后矛盾",
			Detail:   "第二节写「24 个月」，第三节又写「长期有效，不设过期」，两处不能同时成立。",
			NewQuote: "积分有效期为 24 个月,到期自动清零。", NewPath: "二、积分规则",
			OldQuote: "历史积分同样长期有效。", OldPath: "三、存量处理",
			OldDocTitle: "会员积分规则 PRD v2.1",
			Question:    "以哪一处为准？另一处需要怎么改？", Status: "open"},
		{ID: "a1", Type: "ambiguity", Subtype: "unresolved_reference", Severity: "S1", Confidence: 0.80,
			Title:    "「之前的活动规则」没有可核对的版本",
			Detail:   "「之前的活动规则」没有写明是哪份文档、哪个版本或哪条规则，开发无法确认删改范围。",
			NewQuote: "之前的活动规则不要。", NewPath: "三、存量处理",
			Question: "这里说的是哪份活动规则、哪个版本？保留什么、删除什么？",
			Status:   "confirmed"},
		{ID: "o1", Type: "one_liner", Subtype: "one_sentence", Severity: "S2", Confidence: 0.62,
			Title:    "一句话需求：支持导出",
			Detail:   "这条需求只有一句话，没有写触发条件、边界或验收标准；开发只能自行理解，测试也无法判定是否完成。",
			NewQuote: "支持导出。", NewPath: "二、积分规则",
			Question: "导出哪些字段、给谁导出、失败时怎么提示？", Status: "open"},
		{ID: "p1", Type: "pre_review", Subtype: "data_compat", Severity: "S2", Confidence: 0.74,
			Title:    "缺少存量积分的迁移规则",
			Detail:   "文档只写了新积分规则，没有说明存量积分如何迁移、重算或兼容。",
			NewQuote: "历史积分同样长期有效。", NewPath: "三、存量处理",
			Question: "存量积分是刷数、兼容一段时间，还是直接按新规则？", Status: "pending"},
	}
}

func demoPreReview() store.PreReview {
	return store.PreReview{
		Verdict: "clarify",
		Summary: "积分规则的主流程可以读出来，但有 3 条阻断级问题、1 个必备章节缺失：补齐存量处理与验收标准后可进入排期。",
		Risks: []string{
			"S1 · 积分有效期在文档内前后矛盾",
			"S1 · 旧规则被替换，未交代存量积分",
			"S2 · 「之前的活动规则」没有可核对的版本",
		},
		Asked: []string{
			"积分有效期以 24 个月还是长期有效为准？",
			"存量积分按新规则重算，还是只对新增积分生效？",
			"「之前的活动规则」具体指哪份文档、哪个版本？",
			"导出功能导哪些字段、失败时怎么提示？",
		},
	}
}

func demoLedger() []store.ChangeRecord {
	return []store.ChangeRecord{
		{ID: 1, Kind: "modified", Label: "会员 · 积分生命周期 · 有效期", DocID: 1,
			DocTitle: "会员积分规则 PRD v2.1", Status: "unresolved",
			Before: "积分永久有效，不设过期机制。", After: "积分有效期为 24 个月，到期自动清零。",
			Impact: "存量积分与过期任务未交代"},
		{ID: 2, Kind: "baseline", Label: "会员 · 积分获取 · 合并计算", DocID: 1,
			DocTitle: "会员积分规则 PRD v2.1", Status: "extracted",
			After: "签到积分与消费积分合并计算。"},
	}
}
