package agent

import (
	"strings"
	"testing"

	"prior/store"
)

// TestHalveSplitsAChunkGroup covers the retry a cut answer gets: the part is
// cut at a chunk boundary, both halves together are the whole part, and each
// asks for fewer findings than the part did.
func TestHalveSplitsAChunkGroup(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"一"}, Content: strings.Repeat("甲", 100)},
		{Index: 1, Path: []string{"二"}, Content: strings.Repeat("乙", 100)},
		{Index: 2, Path: []string{"三"}, Content: strings.Repeat("丙", 100)},
		{Index: 3, Path: []string{"四"}, Content: strings.Repeat("丁", 100)},
	}
	part := parts(chunks, []string{"全文"})[0]
	left, right := halve(part)
	if len(left.Chunks) == 0 || len(right.Chunks) == 0 {
		t.Fatalf("the halves are %d and %d chunks", len(left.Chunks), len(right.Chunks))
	}
	if len(left.Chunks)+len(right.Chunks) != len(part.Chunks) {
		t.Fatalf("the halves hold %d chunks, the part %d", len(left.Chunks)+len(right.Chunks), len(part.Chunks))
	}
	if left.Text == "" || right.Text == "" {
		t.Fatal("a half has no body")
	}
	if strings.Contains(left.Text, "丁") || strings.Contains(right.Text, "甲") {
		t.Fatal("the halves overlap")
	}
}

// TestPartsKeepTheirChunks covers what a retry needs: each part carries its
// own chunks, in order, so a cut answer can be split again.
func TestPartsKeepTheirChunks(t *testing.T) {
	var chunks []store.Chunk
	for i := range 9 {
		chunks = append(chunks, store.Chunk{Index: i, Content: strings.Repeat("字", PartChars/2)})
	}
	got := parts(chunks, nil)
	if len(got) < 2 {
		t.Fatalf("the document was read in %d part(s)", len(got))
	}
	total := 0
	for i, part := range got {
		if len(part.Chunks) == 0 {
			t.Fatalf("part %d carries no chunks", i)
		}
		for j, c := range part.Chunks {
			if c.Index != total+j {
				t.Fatalf("part %d holds chunk %d where %d was expected", i, c.Index, total+j)
			}
		}
		total += len(part.Chunks)
	}
	if total != len(chunks) {
		t.Fatalf("the parts hold %d chunks, the document %d", total, len(chunks))
	}
}

// TestAskPartSplitsAnUnreadableCut covers the shape the live runs hit: an
// answer cut before its first complete element is reported as an error, and
// the part has to go again over its halves instead of leaving the stage short
// a section.
func TestAskPartSplitsAnUnreadableCut(t *testing.T) {
	chunks := []store.Chunk{
		{Index: 0, Content: strings.Repeat("甲", 400)},
		{Index: 1, Content: strings.Repeat("乙", 400)},
		{Index: 2, Content: strings.Repeat("丙", 400)},
		{Index: 3, Content: strings.Repeat("丁", 400)},
	}
	part := parts(chunks, nil)[0]
	calls := 0
	var seen []string
	var progress []string
	out, err := askPart(AnalyzeOptions{Progress: func(ev Event) { progress = append(progress, ev.Message) }},
		"P", part, partPrompt, func(text string) ([]string, bool, error) {
			calls++
			if calls == 1 {
				// No complete element: the decoder's own way of saying the
				// answer was cut before anything readable.
				return nil, true, errCutAnswer
			}
			seen = append(seen, text)
			return []string{"一条"}, false, nil
		})
	if err != nil {
		t.Fatalf("a cut answer failed the stage: %v", err)
	}
	if calls != 3 {
		t.Fatalf("the stage asked %d times, want the part and its two halves", calls)
	}
	if len(out) != 2 || len(seen) != 2 {
		t.Fatalf("the halves returned %d findings over %d calls", len(out), len(seen))
	}
	if len(progress) == 0 || !strings.Contains(progress[0], "拆成两半") {
		t.Fatalf("the stage said %v", progress)
	}
}

// TestAskPartKeepsASingleChunk covers a part that cannot be split: the cut is
// reported rather than retried for ever.
func TestAskPartKeepsASingleChunk(t *testing.T) {
	part := parts([]store.Chunk{{Index: 0, Content: strings.Repeat("甲", 9000)}}, nil)[0]
	calls := 0
	_, err := askPart(AnalyzeOptions{}, "P", part, partPrompt, func(string) ([]string, bool, error) {
		calls++
		return nil, true, errCutAnswer
	})
	if calls != 1 {
		t.Fatalf("a single-chunk part was asked %d times", calls)
	}
	if err == nil {
		t.Fatal("the cut was not reported")
	}
}

// errCutAnswer stands for the decoder's answer-cut error.
var errCutAnswer = errString("回答被截断且没有完整条目")

type errString string

func (e errString) Error() string { return string(e) }
