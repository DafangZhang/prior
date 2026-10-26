package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prior/embed"
	"prior/llm"
	"prior/store"
)

// TestOneLinersBatchLongDocuments covers the stage's own limit: a document
// with more requirements than one answer can carry is read in batches, and a
// batch whose answer comes back cut is asked again over its halves. The old
// stage read every item in one call and kept nothing when the answer was cut.
func TestOneLinersBatchLongDocuments(t *testing.T) {
	var calls int
	var itemCounts []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := readRequestBody(t, r)
		n := strings.Count(body, "- id=")
		itemCounts = append(itemCounts, n)
		// The first call names so many items that its answer is cut: the
		// stage has to ask again over smaller parts.
		if calls == 1 {
			writeModelAnswer(w, fmt.Sprintf(`{"findings":[%s`, strings.Repeat(
				`{"title":"一句话需求","detail":"缺少角色与验收标准","quote":"条目","question":"谁来做？"},`, 8)))
			return
		}
		var findings []string
		for i := range n {
			findings = append(findings, fmt.Sprintf(
				`{"title":"一句话需求 %d","detail":"缺少角色与验收标准","quote":"条目 %d","question":"谁来做？","severity":"S2"}`, i, i))
		}
		writeModelAnswer(w, `{"findings":[`+strings.Join(findings, ",")+`]}`)
	}))
	defer server.Close()

	client := embed.New("", "bge-m3:latest", llm.New(llm.Config{Protocol: llm.ProtocolOpenAI, Endpoint: server.URL, Model: "m"}))
	items := make([]Item, 60)
	for i := range items {
		items[i] = Item{ID: itoa(i + 1), Quote: fmt.Sprintf("条目 %d", i+1), Need: "做一件事"}
	}
	var stages []string
	findings, err := oneLiners(context.Background(), client, AnalyzeOptions{
		Doc:      testDoc("长文档"),
		Progress: func(ev Event) { stages = append(stages, ev.Message) },
	}, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemCounts) < 3 {
		t.Fatalf("the stage made %d calls for %d items: %v", len(itemCounts), len(items), itemCounts)
	}
	if itemCounts[0] > itemsPerCall {
		t.Errorf("the first call read %d items, more than %d", itemCounts[0], itemsPerCall)
	}
	if len(findings) == 0 {
		t.Fatal("the stage kept no findings")
	}
	for _, f := range findings {
		if f.Type != "one_liner" {
			t.Errorf("a finding is filed as %q", f.Type)
		}
	}
	t.Logf("calls %v, findings %d, stages %v", itemCounts, len(findings), stages)
}

// TestOneLinersSplitACutBatch covers the retry itself: a cut answer over a
// batch goes again over each half, and the cut answer's own entries are not
// counted twice.
func TestOneLinersSplitACutBatch(t *testing.T) {
	var itemCounts []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readRequestBody(t, r)
		n := strings.Count(body, "- id=")
		itemCounts = append(itemCounts, n)
		if len(itemCounts) == 1 {
			writeModelAnswer(w, `{"findings":[{"title":"半条","detail":"d","quote":"q","question":"?"}`)
			return
		}
		writeModelAnswer(w, `{"findings":[{"title":"一条","detail":"d","quote":"q","question":"?"}]}`)
	}))
	defer server.Close()
	client := embed.New("", "bge-m3:latest", llm.New(llm.Config{Protocol: llm.ProtocolOpenAI, Endpoint: server.URL, Model: "m"}))
	items := make([]Item, 6)
	for i := range items {
		items[i] = Item{ID: itoa(i + 1), Quote: fmt.Sprintf("条目 %d", i+1)}
	}
	findings, err := oneLiners(context.Background(), client, AnalyzeOptions{Doc: testDoc("文档")}, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemCounts) != 3 {
		t.Fatalf("the stage made %d calls, want the batch and its two halves: %v", len(itemCounts), itemCounts)
	}
	if len(findings) != 2 {
		t.Fatalf("the stage kept %d findings, want the halves' own: %+v", len(findings), findings)
	}
}

// readRequestBody is the request the model was sent, for the stub to count
// the items in it.
func readRequestBody(t *testing.T, r *http.Request) string {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// writeModelAnswer answers as an OpenAI-compatible endpoint does.
func writeModelAnswer(w http.ResponseWriter, answer string) {
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"content": answer},
	}}})
	w.Write(data)
}

// testDoc is the document the stage is told it is reading.
func testDoc(title string) store.Doc {
	return store.Doc{ID: 1, Title: title, Module: "会员"}
}

// TestOneLinersSplitAnUnreadableCut covers the shape the live run hit: an
// answer cut before its first complete entry comes back as an error, and the
// batch has to be asked again over its halves rather than reported failed.
func TestOneLinersSplitAnUnreadableCut(t *testing.T) {
	var calls int
	var itemCounts []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := readRequestBody(t, r)
		itemCounts = append(itemCounts, strings.Count(body, "- id="))
		if calls == 1 {
			// No complete element: the array's first object is half-written.
			writeModelAnswer(w, `{"findings":[{"title":"半条","detail":"写到一半`)
			return
		}
		writeModelAnswer(w, `{"findings":[{"title":"一条","detail":"d","quote":"q","question":"?"}]}`)
	}))
	defer server.Close()
	client := embed.New("", "bge-m3:latest", llm.New(llm.Config{Protocol: llm.ProtocolOpenAI, Endpoint: server.URL, Model: "m"}))
	items := make([]Item, 4)
	for i := range items {
		items[i] = Item{ID: itoa(i + 1), Quote: "条目 " + itoa(i+1)}
	}
	findings, err := oneLiners(context.Background(), client, AnalyzeOptions{Doc: testDoc("文档")}, items)
	if err != nil {
		t.Fatalf("an answer cut before its first entry failed the stage: %v", err)
	}
	if len(itemCounts) != 3 {
		t.Fatalf("calls %v, want the batch and its two halves", itemCounts)
	}
	if len(findings) != 2 {
		t.Fatalf("kept %d findings from the halves", len(findings))
	}
}
