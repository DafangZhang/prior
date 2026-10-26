package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prior/embed"
	"prior/llm"
	"prior/store"
)

// TestLiveConflictSurvivesTheConstraint covers the conflict finding from the
// ledger to the report's own constraint: a changed rule the document does not
// explain has to survive enforce as a logic gap with the old rule as its
// evidence. It used to be rewritten into an "undefined reference" ambiguity,
// because the ledger's evidence names its document without a chunk id.
func TestLiveConflictSurvivesTheConstraint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	st, err := store.Open(ctx, "postgres://postgres:postgres@127.0.0.1:5433/prior")
	if err != nil {
		t.Skipf("PostgreSQL 不可用：%v", err)
	}
	defer st.Close()

	// The module is this test's own, and it starts empty: a point left by an
	// earlier failing run would be matched as the same rule, and the
	// ledger's "before" would be that run's text.
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	module := "冲突用例·" + stamp
	cleanup := func() {
		_, _ = st.Pool().Exec(context.Background(), `DELETE FROM function_points WHERE module LIKE '冲突用例·%'`)
	}
	cleanup()
	t.Cleanup(cleanup)
	oldRule := "退款" + stamp + "在 7 个工作日内原路退回"
	base := store.Doc{
		Title: "冲突基线 " + stamp, SourceURI: "test/冲突基线.md", Format: "md", DocType: "requirement",
		Module: module, Status: "active", Checksum: "conflict-base-" + stamp, Chars: 40,
		EmbedModel: "bge-m3:latest", Date: time.Date(2025, 1, 10, 0, 0, 0, 0, time.Local),
		DateSource: "field", DateConf: 0.9,
	}
	baseID, err := st.SaveDocument(ctx, base, []store.Chunk{
		{Index: 0, Path: []string{"一、规则"}, Content: oldRule + "。", Tokens: 12},
	}, [][]float32{testUnitVector(5)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Delete(context.Background(), baseID) })
	pointID, err := st.UpsertFunctionPoint(ctx, store.FunctionPoint{
		Module: module, Function: "退款" + stamp, Topic: "时限",
		Label: module + " · 退款 · 时限",
	}, baseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveChangeRecord(ctx, store.ChangeRecord{
		PointID: pointID, DocID: baseID, Kind: "baseline", After: oldRule,
		Evidence: []store.Evidence{{DocID: baseID, DocTitle: base.Title, Quote: oldRule}},
		Status:   "extracted", Recorded: base.Date,
	}); err != nil {
		t.Fatal(err)
	}

	// The new document states the changed rule, says nothing about the old
	// one, and the model answers with that rule as a function point.
	newRule := "退款" + stamp + "在 3 个工作日内原路退回"
	chunks := []store.Chunk{{Index: 0, Path: []string{"一、规则"}, Content: newRule + "。"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		answer := `{"function_points":[{"module":"` + module + `","function":"退款","topic":"时限",` +
			`"rule":"` + newRule + `","quote":"` + newRule + `","heading_path":["一、规则"],"impact":""}]}`
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": answer},
		}}})
		w.Write(data)
	}))
	defer server.Close()
	em := embed.New("", "bge-m3:latest", llm.New(llm.Config{
		Protocol: llm.ProtocolOpenAI, Endpoint: server.URL, Model: "stub",
	}))

	newDoc := store.Doc{ID: baseID, Title: "冲突新版 " + stamp, Module: module,
		Date: time.Date(2026, 1, 10, 0, 0, 0, 0, time.Local), DateSource: "field"}
	opts := AnalyzeOptions{
		Doc: newDoc, Chunks: chunks, LLMModel: "stub",
		Profile:  Profile{Module: module, Headings: []string{"一、规则"}},
		Progress: func(ev Event) { t.Logf("  %s %s", ev.Stage, ev.Message) },
	}
	runID, err := st.StartRun(ctx, baseID, newDoc.Title, map[string]any{"llm": "stub"})
	if err != nil {
		t.Fatal(err)
	}
	records, points, conflicts, err := writeLedger(ctx, st, em, opts, runID)
	if err != nil {
		t.Fatal(err)
	}
	if records == 0 || points == 0 {
		t.Fatalf("the ledger wrote %d records over %d points", records, points)
	}
	if len(conflicts) != 1 {
		t.Fatalf("the changed rule with no transition made %d conflict findings, want 1", len(conflicts))
	}
	conflict := conflicts[0]
	if conflict.Type != "logic_gap" || conflict.Subtype != "unresolved_conflict" {
		t.Fatalf("the conflict reads %s/%s", conflict.Type, conflict.Subtype)
	}
	if !strings.Contains(conflict.Detail, oldRule) {
		t.Errorf("the finding does not cite the old rule: %q", conflict.Detail)
	}
	// The ledger row itself says the change is unresolved.
	recs, err := st.ChangeRecords(ctx, pointID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 || recs[0].Kind != "conflicted" || recs[0].Status != "unresolved" {
		t.Fatalf("the ledger's newest entry reads %s/%s", recs[0].Kind, recs[0].Status)
	}

	// Through the constraint: a logic gap keeps its history, and this one's
	// history is the ledger's own entry.
	kept := enforce(conflicts, chunks)
	if len(kept) != 1 {
		t.Fatalf("the constraint dropped the conflict: %d of %d", len(kept), len(conflicts))
	}
	if kept[0].Type != "logic_gap" || kept[0].Subtype != "unresolved_conflict" {
		t.Fatalf("the constraint rewrote the conflict as %s/%s", kept[0].Type, kept[0].Subtype)
	}
	if len(kept[0].Evidence) == 0 || kept[0].Evidence[0].Quote == "" {
		t.Fatalf("the conflict lost its evidence: %+v", kept[0].Evidence)
	}
	t.Logf("conflict: %s | evidence %q from %q", kept[0].Title, kept[0].Evidence[0].Quote, kept[0].Evidence[0].DocTitle)
}

func testUnitVector(at int) []float32 {
	v := make([]float32, 1024)
	v[at] = 1
	return v
}
