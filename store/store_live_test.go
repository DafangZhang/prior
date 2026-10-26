package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"prior/store"
)

// dsn is the database the tests use, the one this machine runs.
func dsn() string {
	if v := os.Getenv("PRIOR_DSN"); v != "" {
		return v
	}
	return "postgres://postgres:postgres@127.0.0.1:5433/prior"
}

func open(t *testing.T) *store.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := store.Open(ctx, dsn())
	if err != nil {
		t.Skipf("PostgreSQL 不可用：%v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// unitVector is a one-hot vector of the schema's width.
func unitVector(at int) []float32 {
	v := make([]float32, 1024)
	v[at] = 1
	return v
}

func TestOpenMigratesAndStores(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	doc := store.Doc{
		Title: "测试文档 · 积分过期规则", SourceURI: "test/积分.md", Format: "md",
		DocType: "requirement", Module: "会员", Status: "active",
		Checksum: "test-checksum-1", Chars: 120, EmbedModel: "bge-m3:latest",
		Date: time.Date(2025, 11, 20, 0, 0, 0, 0, time.Local), DateSource: "field", DateConf: 0.95,
	}
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"一、版本信息"}, Content: "积分有效期由永久改为 24 个月", Tokens: 20},
		{Index: 1, Path: []string{"二、积分生命周期"}, Content: "积分过期后自动清零，不设延期", Tokens: 18},
	}
	// The vectors are the width the schema stores: with pgvector the column
	// is vector(1024), and a toy three-dimensional vector would be rejected
	// there rather than exercised.
	vectors := [][]float32{unitVector(0), unitVector(1)}
	id, err := s.SaveDocument(ctx, doc, chunks, vectors)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("no document id")
	}
	// The same checksum writes once: the second save keeps one document.
	if _, err := s.SaveDocument(ctx, doc, chunks, vectors); err != nil {
		t.Fatal(err)
	}
	// The knowledge base may hold the real corpus, so the assertions look
	// this document up by its checksum rather than reading the first page.
	if _, ok, err := s.DocumentByChecksum(ctx, doc.Checksum); err != nil || !ok {
		t.Fatalf("文档没有落库：ok=%v err=%v", ok, err)
	}
	all, err := s.ListDocuments(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, d := range all {
		if d.Checksum != doc.Checksum {
			continue
		}
		found++
		if d.Module != "会员" || d.DateSource != "field" {
			t.Fatalf("元数据没落库：%+v", d)
		}
		if d.Chunks != 2 {
			t.Fatalf("期望 2 块，得到 %d", d.Chunks)
		}
	}
	if found != 1 {
		t.Fatalf("期望 1 份文档，得到 %d", found)
	}

	// Hybrid recall: the vector half finds the first chunk, the keyword half
	// the second, and the fusion keeps both.
	vecHits, err := s.SearchVector(ctx, unitVector(0), 5)
	if err != nil {
		t.Fatal(err)
	}
	kwHits, err := s.SearchKeyword(ctx, "积分过期", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecHits) == 0 || len(kwHits) == 0 {
		t.Fatalf("检索为空：vector=%d keyword=%d", len(vecHits), len(kwHits))
	}
	fused := store.Fuse(60, vecHits, kwHits)
	if len(fused) < 2 {
		t.Fatalf("融合后期望至少 2 条，得到 %d", len(fused))
	}
	t.Logf("vector=%d keyword=%d fused=%d 首条=%q", len(vecHits), len(kwHits), len(fused), fused[0].Content)

	// Clean up what this test wrote.
	if _, err := s.Pool().Exec(ctx, `DELETE FROM documents WHERE checksum = $1`, doc.Checksum); err != nil {
		t.Fatal(err)
	}
}

// TestSaveDocumentReplacesByPath covers a file read again after the parser
// changed: its path is the same and its checksum is not, and it has to
// replace the document rather than become a second copy of it.
func TestSaveDocumentReplacesByPath(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	base := store.Doc{
		Title: "测试 · 同一文件两种读法", SourceURI: "test/reparse.md", Format: "md",
		DocType: "requirement", Module: "会员", Status: "active",
		Checksum: "reparse-first", Chars: 30, EmbedModel: "bge-m3:latest",
	}
	first, err := s.SaveDocument(ctx, base, []store.Chunk{
		{Index: 0, Content: "<span style=\"x\">旧读法</span>", Tokens: 8},
	}, [][]float32{unitVector(11)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.Delete(context.Background(), first) })
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(),
			`DELETE FROM documents WHERE source_uri = 'test/reparse.md'`)
	})

	// The same file, read again with a cleaner parser: same path, new
	// checksum, different text.
	second := base
	second.Checksum = "reparse-second"
	second.Chars = 10
	id, err := s.SaveDocument(ctx, second, []store.Chunk{
		{Index: 0, Content: "新读法", Tokens: 4},
	}, [][]float32{unitVector(12)})
	if err != nil {
		t.Fatal(err)
	}
	if id != first {
		t.Fatalf("the re-read file became document %d, the first was %d", id, first)
	}
	chunks, err := s.DocumentChunks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Content != "新读法" {
		t.Fatalf("the chunks read %+v", chunks)
	}
	// There is one document for the path, not two.
	var n int
	if err := s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM documents WHERE source_uri = 'test/reparse.md'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d documents hold the path, want 1", n)
	}
	// The checksum moved with the text, so the file is not skipped next time
	// it is seen unchanged.
	if found, ok, err := s.DocumentByChecksum(ctx, "reparse-second"); err != nil || !ok || found.ID != id {
		t.Fatalf("the new checksum reads %+v ok=%v err=%v", found, ok, err)
	}
}
