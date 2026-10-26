package main

import (
	"flag"
	"testing"
)

// TestParseArgsFlagsAfterPositional covers the shape a user types:
// `analyze doc.md -out r.md`, where flag.Parse would drop -out.
func TestParseArgsFlagsAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	out := fs.String("out", "", "path")
	noRerank := fs.Bool("no-rerank", false, "skip")
	rest, err := parseArgs(fs, []string{"产品文档/a.docx", "-out", "/tmp/r.md", "-no-rerank"})
	if err != nil {
		t.Fatal(err)
	}
	if *out != "/tmp/r.md" || !*noRerank {
		t.Errorf("flags after the document were dropped: out=%q no-rerank=%v", *out, *noRerank)
	}
	if len(rest) != 1 || rest[0] != "产品文档/a.docx" {
		t.Errorf("positional arguments are %v", rest)
	}
}

// TestParseArgsForms covers the ordinary spellings.
func TestParseArgsForms(t *testing.T) {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	dsn := fs.String("dsn", "default", "dsn")
	quiet := fs.Bool("q", false, "quiet")
	rest, err := parseArgs(fs, []string{"-dsn=custom", "dir", "-q"})
	if err != nil {
		t.Fatal(err)
	}
	if *dsn != "custom" || !*quiet || len(rest) != 1 || rest[0] != "dir" {
		t.Errorf("got dsn=%q q=%v rest=%v", *dsn, *quiet, rest)
	}
	rest, err = parseArgs(fs, []string{"-dsn", "one", "--", "-not-a-flag"})
	if err != nil || *dsn != "one" || len(rest) != 1 || rest[0] != "-not-a-flag" {
		t.Errorf("-- is not respected: dsn=%q rest=%v err=%v", *dsn, rest, err)
	}
}
