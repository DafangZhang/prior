package store_test

import (
	"context"
	"testing"
	"time"

	"prior/store"
)

// TestLedgerReadsDocumentsInOrder covers how a function point's trail is read:
// by the date of the document each entry came from, and by the version when
// two documents share a day. A document with no date of its own is never read
// as the newest one, however late it was recorded.
func TestLedgerReadsDocumentsInOrder(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	module, function, topic := "测试模块·排序", "功能点排序", "顺序"
	save := func(checksum, title string, date time.Time, source string, rank int) int64 {
		t.Helper()
		doc := store.Doc{
			Title: title, SourceURI: "test/" + title + ".md", Format: "md", DocType: "requirement",
			Module: module, Status: "active", Checksum: checksum, Chars: 80, EmbedModel: "bge-m3:latest",
			Date: date, DateSource: source, DateConf: 0.9, VersionRank: rank,
		}
		if source == "missing" {
			doc.Date = time.Time{}
		}
		chunks := []store.Chunk{{Index: 0, Content: title + " 的规则", Tokens: 8}}
		id, err := s.SaveDocument(ctx, doc, chunks, [][]float32{unitVector(3)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	older := save("test-order-older", "排序测试·旧版 V1.0.0", date(2023, 9, 20), "field", 1000)
	newer := save("test-order-newer", "排序测试·新版 V1.1.0", date(2023, 10, 26), "field", 1010)
	undated := save("test-order-undated", "排序测试·无日期", time.Time{}, "missing", -1)

	pointID, err := s.UpsertFunctionPoint(ctx, store.FunctionPoint{
		Module: module, Function: function, Topic: topic, Label: "排序测试",
	}, older)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.Pool().Exec(context.Background(),
			`DELETE FROM change_records WHERE function_point_id = $1`, pointID); err != nil {
			t.Logf("清理账本失败：%v", err)
		}
		if _, err := s.Pool().Exec(context.Background(),
			`DELETE FROM function_points WHERE id = $1`, pointID); err != nil {
			t.Logf("清理功能点失败：%v", err)
		}
		if _, err := s.Pool().Exec(context.Background(),
			`DELETE FROM documents WHERE checksum LIKE 'test-order-%'`); err != nil {
			t.Logf("清理文档失败：%v", err)
		}
	})

	// The undated document is written last, so a reader that sorts by when
	// rows were recorded would take it for the newest rule.
	write := func(docID int64, rule string, when time.Time) {
		t.Helper()
		if _, err := s.SaveChangeRecord(ctx, store.ChangeRecord{
			PointID: pointID, DocID: docID, Kind: "modified", After: rule, Status: "extracted",
			Recorded: when,
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(older, "规则 A", date(2023, 9, 20))
	write(newer, "规则 B", date(2023, 10, 26))
	write(undated, "规则 C", time.Now())

	before := func(at time.Time, dated bool) string {
		t.Helper()
		rec, ok, err := s.LatestRecordBefore(ctx, pointID, at, dated)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("没有找到 %s 之前的记录", at.Format("2006-01-02"))
		}
		return rec.After
	}
	if got := before(date(2023, 9, 25), true); got != "规则 A" {
		t.Errorf("2023-09-25 之前 = %q；want 规则 A", got)
	}
	if got := before(date(2023, 11, 1), true); got != "规则 B" {
		t.Errorf("2023-11-01 之前 = %q；want 规则 B", got)
	}
	// A document whose date is unknown takes the newest rule there is, and
	// never the rule of another undated document.
	if got := before(time.Time{}, false); got != "规则 B" {
		t.Errorf("无日期文档之前 = %q；want 规则 B", got)
	}

	// The registry's rule for the point is the newest document's, not the
	// last row written.
	points, err := s.FunctionPoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range points {
		if p.ID == pointID && p.LatestRule != "规则 B" {
			t.Errorf("功能点当前规则 = %q；want 规则 B", p.LatestRule)
		}
	}

	// The trail reads newest first, and the undated document comes last.
	records, err := s.ChangeRecords(ctx, pointID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("账本 %d 条；want 3", len(records))
	}
	if records[0].After != "规则 B" || records[1].After != "规则 A" || records[2].After != "规则 C" {
		t.Errorf("账本顺序 = %q, %q, %q；want 规则 B, 规则 A, 规则 C",
			records[0].After, records[1].After, records[2].After)
	}

	// Two documents of one day read from the older version to the newer one.
	sameDay := save("test-order-sameday", "排序测试·同日 V1.0.0", date(2023, 10, 26), "field", 1000)
	write(sameDay, "规则 D", date(2023, 10, 26))
	if got := before(date(2023, 10, 26), true); got != "规则 B" {
		t.Errorf("同一天里 V1.1.0 应在新写的 V1.0.0 之后；得到 %q", got)
	}
	records, err = s.ChangeRecords(ctx, pointID)
	if err != nil {
		t.Fatal(err)
	}
	if records[0].After != "规则 B" || records[1].After != "规则 D" {
		t.Errorf("同一天的两条 = %q, %q；want 规则 B, 规则 D", records[0].After, records[1].After)
	}

	// The report's ledger holds the dated entries above the undated one.
	ledger, err := s.Ledger(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	place := map[string]int{}
	for i, rec := range ledger {
		if rec.PointID == pointID {
			place[rec.After] = i
		}
	}
	if len(place) != 4 {
		t.Fatalf("账本里本方功能点只有 %d 条；want 4", len(place))
	}
	if place["规则 C"] < place["规则 D"] || place["规则 C"] < place["规则 A"] || place["规则 C"] < place["规则 B"] {
		t.Errorf("无日期的一条排到了有日期的前面：%v", place)
	}
}

// TestMergeReadsHistoryInOrder covers the re-read after two function points are
// merged: the history is read by the documents' own dates, so the entry that
// was a baseline turns into the change it turned out to be, and a later
// document's entry is never read as the earlier one. The recorded times are
// set against the dates on purpose: were the re-read to sort by them, the two
// entries would come out the other way round.
func TestMergeReadsHistoryInOrder(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	module := "测试模块·合并"
	save := func(checksum, title string, date time.Time) int64 {
		t.Helper()
		doc := store.Doc{
			Title: title, SourceURI: "test/" + title + ".md", Format: "md", DocType: "requirement",
			Module: module, Status: "active", Checksum: checksum, Chars: 60, EmbedModel: "bge-m3:latest",
			Date: date, DateSource: "field", DateConf: 0.9,
		}
		id, err := s.SaveDocument(ctx, doc, []store.Chunk{{Index: 0, Content: title, Tokens: 8}},
			[][]float32{unitVector(4)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	older := save("test-merge-older", "合并测试·旧文档", date(2023, 9, 20))
	newer := save("test-merge-newer", "合并测试·新文档", date(2023, 10, 26))
	keep, err := s.UpsertFunctionPoint(ctx, store.FunctionPoint{
		Module: module, Function: "合并目标", Topic: "顺序", Label: "合并目标",
	}, newer)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := s.UpsertFunctionPoint(ctx, store.FunctionPoint{
		Module: module, Function: "被合并", Topic: "顺序", Label: "被合并",
	}, older)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, query := range []string{
			`DELETE FROM change_records WHERE function_point_id IN (SELECT id FROM function_points WHERE module = '测试模块·合并')`,
			`DELETE FROM function_points WHERE module = '测试模块·合并'`,
			`DELETE FROM documents WHERE checksum LIKE 'test-merge-%'`,
		} {
			if _, err := s.Pool().Exec(bg, query); err != nil {
				t.Logf("清理失败：%v", err)
			}
		}
	})
	// The target point's entry comes from the newer document but was recorded
	// earlier, and the moved point's the other way round.
	write := func(pointID, docID int64, rule string, when time.Time) {
		t.Helper()
		if _, err := s.SaveChangeRecord(ctx, store.ChangeRecord{
			PointID: pointID, DocID: docID, Kind: "baseline", After: rule, Status: "extracted",
			Recorded: when,
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(keep, newer, "新文档的规则", date(2023, 9, 20))
	write(moved, older, "旧文档的规则", date(2023, 10, 26))

	if n, err := s.MergeFunctionPoints(ctx, moved, keep); err != nil || n != 1 {
		t.Fatalf("合并 %d 条，err=%v", n, err)
	}
	records, err := s.ChangeRecords(ctx, keep)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("合并后账本 %d 条；want 2", len(records))
	}
	if records[0].After != "新文档的规则" || records[0].Kind != "modified" || records[0].Before != "旧文档的规则" {
		t.Errorf("新文档一条读成 %+v；want modified，before=旧文档的规则", records[0])
	}
	if records[1].After != "旧文档的规则" || records[1].Kind != "baseline" {
		t.Errorf("旧文档一条读成 %+v；want baseline", records[1])
	}
}

// TestSetDocumentDateMovesItsRecords covers the day a person gives a
// document: the entries that carry the document's own date move with it, so
// the trail is read by the day the document was written rather than the day it
// was already stored under.
func TestSetDocumentDateMovesItsRecords(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	docID, err := s.SaveDocument(ctx, store.Doc{
		Title: "手工补录测试", SourceURI: "test/manual-date.md", Format: "md", DocType: "requirement",
		Module: "测试模块·补录", Status: "active", Checksum: "test-manual-date", Chars: 40,
		EmbedModel: "bge-m3:latest", Date: date(2023, 9, 20), DateSource: "file_name_short", DateConf: 0.5,
	}, []store.Chunk{{Index: 0, Content: "规则", Tokens: 4}}, [][]float32{unitVector(5)})
	if err != nil {
		t.Fatal(err)
	}
	pointID, err := s.UpsertFunctionPoint(ctx, store.FunctionPoint{
		Module: "测试模块·补录", Function: "补录日期", Topic: "移动",
	}, docID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveChangeRecord(ctx, store.ChangeRecord{
		PointID: pointID, DocID: docID, Kind: "baseline", After: "规则", Status: "extracted",
		Recorded: date(2023, 9, 20),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		s.Pool().Exec(bg, `DELETE FROM change_records WHERE function_point_id=$1`, pointID)
		s.Pool().Exec(bg, `DELETE FROM function_points WHERE id=$1`, pointID)
		s.Pool().Exec(bg, `DELETE FROM documents WHERE checksum='test-manual-date'`)
	})

	if err := s.SetDocumentDate(ctx, docID, date(2023, 10, 26)); err != nil {
		t.Fatal(err)
	}
	docs, err := s.ListDocuments(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.ID != docID {
			continue
		}
		if d.Date.Format("2006-01-02") != "2023-10-26" || d.DateSource != "manual" || d.DateConf != 1 {
			t.Errorf("补录后文档 = %s / %s / %.2f；want 2023-10-26 / manual / 1.00",
				d.Date.Format("2006-01-02"), d.DateSource, d.DateConf)
		}
	}
	records, err := s.ChangeRecords(ctx, pointID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Recorded.Format("2006-01-02") != "2023-10-26" {
		t.Errorf("记录日期没有跟着走：%+v", records)
	}
	if err := s.SetDocumentDate(ctx, docID+99999, date(2023, 10, 26)); err == nil {
		t.Error("给不存在的文档补录应当报错")
	}
}

// TestRunCountsAndRunLedger covers the two reads the report list and the
// report dialog need: one row of counts per run, and the records one run
// wrote. Both are new SQL, so a live run is what proves the columns line up.
func TestRunCountsAndRunLedger(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	runID, err := s.StartRun(ctx, 0, "测试运行 · 报告列表", map[string]any{"llm": "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM analysis_runs WHERE id = $1`, runID)
	})

	docID, err := s.SaveDocument(ctx, store.Doc{
		Title: "测试运行 · 报告列表文档", SourceURI: "test/run-counts.md", Format: "md",
		DocType: "requirement", Module: "测试模块·运行", Status: "active",
		Checksum: "test-run-counts", Chars: 20, EmbedModel: "bge-m3:latest",
	}, []store.Chunk{{Index: 0, Content: "规则原文", Tokens: 4}}, [][]float32{unitVector(21)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM documents WHERE id = $1`, docID)
	})

	if err := s.SaveFindings(ctx, runID, []store.Finding{
		{Type: "change_signal", Subtype: "changed_rule", Severity: "S1", Confidence: 0.8,
			Title: "一条变更", Detail: "详情", Span: store.Span{Quote: "规则原文"},
			Evidence: []store.Evidence{{DocID: docID, DocTitle: "测试运行 · 报告列表文档", Quote: "规则原文"}}},
		{Type: "pre_review", Subtype: "missing_actor", Severity: "S2", Confidence: 0.7,
			Title: "一个缺口", Detail: "详情", Span: store.Span{Quote: "规则原文"}},
	}); err != nil {
		t.Fatal(err)
	}
	counts, err := s.RunCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := counts[runID]
	if !ok {
		t.Fatalf("RunCounts has no row for run %d", runID)
	}
	if c.Total != 2 || c.Open != 2 || c.Types["change_signal"] != 1 || c.Types["pre_review"] != 1 {
		t.Errorf("counts = %+v", c)
	}
	if c.Severity[1] != 1 || c.Severity[2] != 1 || c.DocsHit != 1 {
		t.Errorf("severity/docs = %+v", c)
	}
	findings, err := s.Findings(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d", len(findings))
	}
	if err := s.SetFindingReview(ctx, findings[0].ID, "confirmed", "同意"); err != nil {
		t.Fatal(err)
	}
	again, err := s.Findings(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range again {
		if f.ID == findings[0].ID && (f.Status != "confirmed" || f.Note != "同意") {
			t.Errorf("review status = %s / %q", f.Status, f.Note)
		}
	}
	counts, err = s.RunCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[runID].Open != 1 {
		t.Errorf("open findings after a review = %d, want 1", counts[runID].Open)
	}

	pointID, err := s.UpsertFunctionPoint(ctx, store.FunctionPoint{
		Module: "测试模块·运行", Function: "报告列表", Topic: "记录", Label: "报告列表",
	}, docID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM change_records WHERE function_point_id = $1`, pointID)
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM function_points WHERE id = $1`, pointID)
	})
	if _, err := s.SaveChangeRecord(ctx, store.ChangeRecord{
		PointID: pointID, DocID: docID, RunID: runID, Kind: "modified",
		Before: "旧规则", After: "新规则", Status: "extracted", Recorded: date(2026, 6, 15),
	}); err != nil {
		t.Fatal(err)
	}
	records, err := s.RunLedger(ctx, runID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RunID != runID || records[0].After != "新规则" {
		t.Errorf("run ledger = %+v", records)
	}
}

// date is a day at midnight, as the library stores dates.
func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}
