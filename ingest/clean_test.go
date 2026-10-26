package ingest_test

import (
	"strings"
	"testing"

	"prior/ingest"
)

// TestCleanTextDropsEditorMarkup covers the markup a .docx carries as literal
// text: the Feishu export writes <span style="…"> inside the Word text runs,
// where the Markdown cleaner never saw it. Normalize cleans every format, so
// the document reads as its author wrote it.
func TestCleanTextDropsEditorMarkup(t *testing.T) {
	blocks := ingest.Normalize([]ingest.Block{
		{Kind: ingest.KindHeading, Level: 1, Text: `一、版本信息`},
		{Kind: ingest.KindParagraph, Text: `<span style="background-color: rgb(248, 249, 250);">版本号:V1.0.0</span>`},
		{Kind: ingest.KindParagraph, Text: `审核人：周明 &amp; 林知远<br>创建日期:2026.06.15`},
		{Kind: ingest.KindParagraph, Text: `比较：1 < 2，且 2 &gt; 1`},
		{Kind: ingest.KindParagraph, Text: `零宽​字符​与 BOM`},
		{Kind: ingest.KindParagraph, Text: `<style>.x{color:red}</style><!-- 导出备注 -->正文保留`},
		{Kind: ingest.KindParagraph, Text: `＜span＞全角标签＜/span＞正文也保留`},
	})
	var got strings.Builder
	for _, b := range blocks {
		got.WriteString(b.Text)
		got.WriteString("\n")
	}
	text := got.String()
	for _, leaked := range []string{"<span", "style=", "<br>", "&amp;", "&gt;", "\u200b", "\ufeff", "color:red", "导出备注"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the normalized text still holds %q: %q", leaked, text)
		}
	}
	if !strings.Contains(text, "版本号:V1.0.0") {
		t.Errorf("the sentence was lost: %q", text)
	}
	if !strings.Contains(text, "周明 & 林知远") {
		t.Errorf("an entity was not decoded: %q", text)
	}
	if !strings.Contains(text, "1 < 2") || !strings.Contains(text, "2 > 1") {
		t.Errorf("a comparison was mistaken for markup: %q", text)
	}
	if !strings.Contains(text, "零宽字符与 BOM") {
		t.Errorf("invisible characters were not dropped: %q", text)
	}
	if !strings.Contains(text, "全角标签正文也保留") {
		t.Errorf("the words around a full-width tag were lost: %q", text)
	}
}

// TestCleanTextIsIdempotent matters because old rows are cleaned again when
// they are read, after the parser already cleaned them.
func TestCleanTextIsIdempotent(t *testing.T) {
	input := `<span style="color: red">规则</span><br>下一行 &amp; 结束`
	once := ingest.CleanText(input)
	twice := ingest.CleanText(once)
	if once != twice {
		t.Fatalf("cleaning twice changed the text:\n%q\n%q", once, twice)
	}
	if strings.Contains(once, "<span") || strings.Contains(once, "&amp;") {
		t.Fatalf("markup survived: %q", once)
	}
}
