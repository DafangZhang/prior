package store_test

import (
	"context"
	"testing"
	"time"

	"prior/store"
)

// TestDeleteTakesTheDocumentAndItsChunks covers what a delete removes and
// what it leaves: the document and its chunks go, and what only pointed at
// it keeps its own row with the reference cleared — a run that reviewed a
// document is still the record of that review after the document is gone.
func TestDeleteTakesTheDocumentAndItsChunks(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	doc := store.Doc{
		Title: "测试文档 · 待删除", SourceURI: "test/待删除.md", Format: "md",
		DocType: "requirement", Module: "会员", Status: "active",
		Checksum: "test-delete-checksum", Chars: 60, EmbedModel: "bge-m3:latest",
		Date: time.Date(2025, 12, 1, 0, 0, 0, 0, time.Local), DateSource: "field", DateConf: 0.9,
	}
	chunks := []store.Chunk{
		{Index: 0, Path: []string{"一、规则"}, Content: "退款申请需在 7 个工作日内处理", Tokens: 16},
		{Index: 1, Path: []string{"二、例外"}, Content: "超过时限的申请需要运营主管审批", Tokens: 16},
	}
	id, err := s.SaveDocument(ctx, doc, chunks, [][]float32{unitVector(7), unitVector(8)})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := s.StartRun(ctx, id, "测试评审 · 待删除", map[string]any{"llm": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, runID, "completed", "", ""); err != nil {
		t.Fatal(err)
	}

	gone, err := s.Delete(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !gone {
		t.Fatal("删除没有报出它删掉了这份文档")
	}
	if _, ok, err := s.DocumentByChecksum(ctx, doc.Checksum); err != nil || ok {
		t.Fatalf("文档还在库里：ok=%v err=%v", ok, err)
	}
	if left, err := s.DocumentChunks(ctx, id); err != nil || len(left) != 0 {
		t.Fatalf("分块没有跟着删掉：%d 块 err=%v", len(left), err)
	}

	// The run survives the document it reviewed, without the reference.
	runs, err := s.Runs(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range runs {
		if r.ID != runID {
			continue
		}
		found = true
		if r.NewDocID != 0 {
			t.Fatalf("评审记录仍指向已删除的文档：%d", r.NewDocID)
		}
	}
	if !found {
		t.Fatal("评审记录被一起删掉了")
	}

	// A document deleted twice is told apart from one never deleted.
	if gone, err := s.Delete(ctx, id); err != nil || gone {
		t.Fatalf("第二次删除：gone=%v err=%v", gone, err)
	}
}
