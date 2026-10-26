package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"prior/embed"
	"prior/llm"
	"prior/store"
)

// TestInternalConflictsKeepsBothSides covers the document's own
// contradiction: both quotes have to be found in this document, and the
// second one is kept as evidence with the document, section and date so the
// report can show the reader where the other statement is. The same sentence
// twice is not a contradiction and is dropped.
func TestInternalConflictsKeepsBothSides(t *testing.T) {
	answer := `{"findings":[
		{"title":"退款时限前后不一致","detail":"3.1 说 7 个工作日，5.2 说 3 个工作日。",
		 "quote":"退款需在 7 个工作日内处理。","evidence":[{"quote":"退款需在 3 个工作日内处理。"}],
		 "question":"退款时限以哪一处为准？","severity":"S1","confidence":0.8},
		{"title":"同一句话出现两次","detail":"两处引用相同。","quote":"退款需在 7 个工作日内处理。",
		 "evidence":[{"quote":"退款需在 7 个工作日内处理。"}],"question":"?","severity":"S2","confidence":0.7}
	]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readRequestBody(t, r)
		writeModelAnswer(w, answer)
	}))
	defer server.Close()
	client := embed.New("", "bge-m3:latest",
		llm.New(llm.Config{Protocol: llm.ProtocolOpenAI, Endpoint: server.URL, Model: "m"}))

	chunks := []store.Chunk{
		{ID: 11, Index: 0, Path: []string{"3.1 退款"}, Content: "退款需在 7 个工作日内处理。"},
		{ID: 12, Index: 1, Path: []string{"5.2 售后"}, Content: "退款需在 3 个工作日内处理。"},
	}
	opts := AnalyzeOptions{
		Doc: store.Doc{ID: 1, Title: "退款规则", Date: time.Date(2026, 6, 15, 0, 0, 0, 0, time.Local),
			DateSource: "field"},
		Chunks:  chunks,
		Profile: Profile{Title: "退款规则", Headings: []string{"3.1 退款", "5.2 售后"}},
	}
	findings, err := internalConflicts(context.Background(), client, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("kept %d findings, want only the real contradiction: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Type != "logic_gap" || f.Subtype != "internal_conflict" {
		t.Errorf("type reads %s/%s", f.Type, f.Subtype)
	}
	if f.Span.Quote != "退款需在 7 个工作日内处理。" {
		t.Errorf("first side reads %q", f.Span.Quote)
	}
	if len(f.Evidence) != 1 {
		t.Fatalf("evidence = %+v", f.Evidence)
	}
	e := f.Evidence[0]
	if e.Quote != "退款需在 3 个工作日内处理。" || e.DocID != 1 || e.DocTitle != "退款规则" ||
		e.ChunkID != 12 || len(e.Path) != 1 || e.Path[0] != "5.2 售后" {
		t.Errorf("the second side is not resolved: %+v", e)
	}
	if e.Date != "2026-06-15" {
		t.Errorf("evidence date = %q", e.Date)
	}
}
