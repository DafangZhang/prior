package agent

import (
	"fmt"
	"strings"
	"testing"

	"prior/ingest"
	"prior/store"
)

// TestPartsSplitLongDocuments covers the split: a document that fits is read
// in one part, a longer one in several, and no chunk is ever cut in two — a
// finding quotes a chunk, and its quote has to be inside one part.
func TestPartsSplitLongDocuments(t *testing.T) {
	short := []store.Chunk{{Index: 0, Path: []string{"一、规则"}, Content: strings.Repeat("字", 200)}}
	if got := parts(short, []string{"一、规则"}); len(got) != 1 || got[0].Total != 1 {
		t.Fatalf("一段能装下的文档被拆成了 %d 段", len(got))
	}

	var long []store.Chunk
	for i := range 12 {
		long = append(long, store.Chunk{
			Index:   i,
			Path:    []string{fmt.Sprintf("第%d节", i+1)},
			Content: fmt.Sprintf("%s【块%d】", strings.Repeat("字", PartChars/3), i),
		})
	}
	got := parts(long, []string{"全文结构"})
	if len(got) < 3 {
		t.Fatalf("%d 个分块的文档被拆成了 %d 段", len(long), len(got))
	}
	for i, part := range got {
		if part.Index != i+1 || part.Total != len(got) {
			t.Fatalf("第 %d 段读成 %d/%d", i, part.Index, part.Total)
		}
		if n := len([]rune(part.Text)); n > PartChars+64 {
			t.Fatalf("第 %d 段有 %d 字，超过 %d", part.Index, n, PartChars)
		}
		if !strings.Contains(part.Text, "全文结构") {
			continue // Headings are carried on the part, not printed into Text
		}
	}
	// Every chunk is in exactly one part, and the parts keep the document's
	// order.
	at := -1
	for i := range long {
		where := -1
		for j, part := range got {
			if strings.Contains(part.Text, fmt.Sprintf("【块%d】", i)) {
				if where >= 0 {
					t.Fatalf("块 %d 出现在两段里", i)
				}
				where = j
			}
		}
		if where < 0 {
			t.Fatalf("块 %d 没有出现在任何一段里", i)
		}
		if where < at {
			t.Fatalf("块 %d 的段落顺序乱了：%d 在 %d 之后", i, where, at)
		}
		at = where
	}
}

// TestPartsCarryTheIngestChunks covers the ingest's own chunks, which are the
// parser's rather than the store's: the same split, from the same document.
func TestPartsCarryTheIngestChunks(t *testing.T) {
	chunks := []ingest.Chunk{
		{Index: 0, HeadingPath: []string{"一、版本信息"}, Content: "版本号 1.0.0"},
		{Index: 1, HeadingPath: []string{"二、规则"}, Content: strings.Repeat("规则", PartChars)},
	}
	got := partsOf(chunks, []string{"一、版本信息", "二、规则"})
	if len(got) != 2 {
		t.Fatalf("分块被拆成了 %d 段", len(got))
	}
	if !strings.Contains(got[0].Text, "版本号 1.0.0") || !strings.Contains(got[1].Text, "### 二、规则") {
		t.Fatalf("段落内容不对：%.40s / %.40s", got[0].Text, got[1].Text)
	}
	if got[0].Headings[0] != "一、版本信息" {
		t.Fatalf("段落没有带上文档结构：%v", got[0].Headings)
	}
}
