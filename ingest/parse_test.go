package ingest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prior/ingest"
)

// corpus returns a file named by an environment variable, skipping when the
// variable or the folder is not there. The corpus is private to the person
// running the test and never ships with the repository.
func corpus(t *testing.T, env string) string {
	t.Helper()
	match := strings.TrimSpace(os.Getenv(env))
	if match == "" {
		t.Skipf("set %s to a file name in 产品文档", env)
	}
	dir, err := filepath.Abs(filepath.Join("..", "产品文档"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skip("the documents folder is not there")
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), match) {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Skipf("no document matching %q", match)
	return ""
}

func TestParseRealDocx(t *testing.T) {
	doc, err := ingest.Parse(corpus(t, "PRIOR_CORPUS_DOCX"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != "docx" {
		t.Fatalf("format is %q", doc.Format)
	}
	if doc.Chars < 200 {
		t.Fatalf("read too little text: %d characters", doc.Chars)
	}
	var headings, tables int
	for _, b := range doc.Blocks {
		switch b.Kind {
		case ingest.KindHeading:
			headings++
		case ingest.KindTable:
			tables++
		}
	}
	if headings == 0 {
		t.Fatal("no headings found")
	}
	t.Logf("title=%q date=%s(%s) headings=%d tables=%d chars=%d warnings=%v",
		doc.Title, doc.Meta.Date.Format("2006-01-02"), doc.Meta.DateSource, headings, tables, doc.Chars, doc.Warnings)
	chunks := ingest.Chunking(doc.Blocks)
	if len(chunks) < 2 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.Tokens > 900 {
			t.Errorf("chunk %d has %d tokens, over the budget", c.Index, c.Tokens)
		}
		if strings.TrimSpace(c.Content) == "" {
			t.Errorf("chunk %d is empty", c.Index)
		}
	}
	t.Logf("chunks=%d first heading path=%v", len(chunks), chunks[0].HeadingPath)
}

func TestParseRealMarkdown(t *testing.T) {
	doc, err := ingest.Parse(corpus(t, "PRIOR_CORPUS_MD"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Chars < 100 {
		t.Fatalf("read too little text: %d", doc.Chars)
	}
	var tables int
	for _, b := range doc.Blocks {
		if b.Kind == ingest.KindTable {
			tables++
		}
	}
	if tables == 0 {
		t.Fatal("the markdown holds a table of agents")
	}
	t.Logf("title=%q date=%s(%s) tables=%d chars=%d", doc.Title, doc.Meta.Date.Format("2006-01-02"), doc.Meta.DateSource, tables, doc.Chars)
}

func TestParseRealXlsx(t *testing.T) {
	doc, err := ingest.Parse(corpus(t, "PRIOR_CORPUS_XLSX"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Title, "属性字段") && !strings.Contains(doc.Title, "媒资") {
		t.Logf("title is %q", doc.Title)
	}
	var rows int
	for _, b := range doc.Blocks {
		if b.Kind == ingest.KindTable {
			rows += len(b.Rows)
		}
	}
	if rows < 5 {
		t.Fatalf("expected a table of fields, got %d rows", rows)
	}
	t.Logf("title=%q rows=%d chars=%d date=%s(%s)", doc.Title, rows, doc.Chars, doc.Meta.Date.Format("2006-01-02"), doc.Meta.DateSource)
}

// TestMarkdownDropsEditorMarkup covers the exports these documents come
// from: Feishu and Word write Markdown with <span style="…"> runs around the
// text, and the parser has to keep the sentence rather than the markup. The
// review quotes what it reads, and a quote of a <span> tag is not the
// document's words.
func TestMarkdownDropsEditorMarkup(t *testing.T) {
	text := "# 版本\n\n<span style=\"background-color: rgb(248, 249, 250);\">版本号:V1.0.0</span>\n\n" +
		"审核人：林知远 &amp; 周明<br>创建日期 260915"
	// The parser is reached through a file, as the pipeline reaches it.
	path := filepath.Join(t.TempDir(), "编辑器导出.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := ingest.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, b := range doc.Blocks {
		body.WriteString(b.Text)
		body.WriteString("\n")
	}
	got := body.String()
	for _, leaked := range []string{"<span", "</span>", "style=", "<br>", "&amp;"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the parsed text still holds %q: %q", leaked, got)
		}
	}
	for _, want := range []string{"版本号:V1.0.0", "林知远 & 周明", "260915"} {
		if !strings.Contains(got, want) {
			t.Errorf("the parsed text lost %q: %q", want, got)
		}
	}
}

// TestMarkdownTableCellsAreCleaned covers a table exported by the editors:
// its cells carry the same <span> runs as its paragraphs, and a reader of the
// table must see the cell's words rather than the markup around them.
func TestMarkdownTableCellsAreCleaned(t *testing.T) {
	text := "| <span style=\"background-color: rgb(242, 243, 245);\">**字段**</span> | 类型 |\n" +
		"| --- | --- |\n" +
		"| <span style=\"background-color: rgb(242, 243, 245);\">ref_image</span> | string |\n"
	path := filepath.Join(t.TempDir(), "表格.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := ingest.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	var cells []string
	for _, b := range doc.Blocks {
		for _, row := range b.Rows {
			cells = append(cells, row...)
		}
	}
	if len(cells) == 0 {
		t.Fatal("the table did not parse")
	}
	for _, cell := range cells {
		if strings.Contains(cell, "<span") || strings.Contains(cell, "style=") {
			t.Errorf("a cell still holds markup: %q", cell)
		}
	}
	if cells[0] != "字段" || cells[2] != "ref_image" {
		t.Errorf("the cells read %q", cells)
	}
}
