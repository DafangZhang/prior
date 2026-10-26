// Package ingest turns the documents a product team writes into the
// blocks, metadata and chunks the knowledge base stores. It reads OOXML
// (.docx, .xlsx), Markdown and plain text, and it never imports the ui
// package: the interface layer consumes its results.
package ingest

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Kind is what a block is.
type Kind uint8

const (
	KindHeading Kind = iota
	KindParagraph
	KindListItem
	KindTable
	KindQuote
	KindCode
)

// Block is one piece of a document, in order: the intermediate
// representation every format parses into.
type Block struct {
	Kind Kind
	// Level is a heading's depth, 1 for a document's first level.
	Level int
	Text  string
	// Rows is a table's cells, row by row.
	Rows [][]string
	// Ordered says a list item is numbered rather than bulleted.
	Ordered bool
}

// Text renders a block as the text the knowledge base stores.
func (b Block) Plain() string {
	if b.Kind == KindTable {
		return tableText(b.Rows)
	}
	return b.Text
}

// tableText renders a table's rows as the pipeline stores them: one line a
// row, its cells separated, as "列 | 列" under the row's first cell.
func tableText(rows [][]string) string {
	var out []string
	for _, row := range rows {
		cells := make([]string, 0, len(row))
		for _, c := range row {
			cells = append(cells, strings.TrimSpace(c))
		}
		line := strings.Join(cells, " | ")
		if line = strings.Trim(line, " |"); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// Doc is a parsed document: its blocks, its metadata, and the text they
// add up to.
type Doc struct {
	Path     string
	Title    string
	Format   string
	Blocks   []Block
	Meta     Meta
	Checksum string
	Chars    int
	// Warnings are what the parser could not do, as for a scanned PDF.
	Warnings []string
}

// Parse reads one document from disk, by its extension.
func Parse(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(path, data)
}

// ParseBytes parses a document held in memory, which tests use.
func ParseBytes(path string, data []byte) (*Doc, error) {
	format := formatOf(path)
	doc := &Doc{Path: path, Format: format}
	var blocks []Block
	var err error
	switch format {
	case "docx":
		if !isZip(data) {
			return nil, fmt.Errorf("ingest: %s 不是有效的 docx（导出或下载失败，其中是 %s）", filepath.Base(path), sniff(data))
		}
		blocks, err = parseDocx(data)
	case "xlsx":
		if !isZip(data) {
			return nil, fmt.Errorf("ingest: %s 不是有效的 xlsx（导出或下载失败）", filepath.Base(path))
		}
		blocks, err = parseXlsx(data)
	case "md", "markdown":
		blocks = parseMarkdown(string(data))
	case "txt", "csv":
		blocks = parseText(string(data))
	default:
		return nil, fmt.Errorf("ingest: %s is not a format the pipeline reads", format)
	}
	if err != nil {
		return nil, err
	}
	doc.Blocks = Normalize(blocks)
	doc.Meta = ExtractMeta(path, doc.Blocks)
	doc.Title = doc.Meta.Title
	doc.Checksum = checksum(doc.Blocks)
	doc.Chars = 0
	for _, b := range doc.Blocks {
		doc.Chars += len([]rune(b.Plain()))
	}
	if doc.Chars < minChars {
		doc.Warnings = append(doc.Warnings, "文本层过短，可能是扫描件或缺文字层的导出文件")
	}
	return doc, nil
}

// ParserVersion names this parser's behaviour, recorded with every job so
// a change in parsing is visible in the data it produced.
const ParserVersion = "parser-2"

// minChars is the least a document needs to be worth indexing, as the
// pipeline's rules say a document below it is refused.
const minChars = 100

// isZip says whether data is an OOXML package, which is a zip.
func isZip(data []byte) bool {
	return len(data) > 3 && data[0] == 'P' && data[1] == 'K'
}

// sniff names what a file holds, for the message of a failed read.
func sniff(data []byte) string {
	head := strings.TrimSpace(string(data))
	if len(head) > 80 {
		head = head[:80]
	}
	if head == "" {
		return "空文件"
	}
	return head
}

// Supported says whether a path is a format the pipeline reads.
func Supported(path string) bool {
	switch formatOf(path) {
	case "docx", "xlsx", "md", "markdown", "txt", "csv":
		return true
	}
	return false
}

func formatOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return strings.TrimPrefix(ext, ".")
}

func checksum(blocks []Block) string {
	sum := sha256.New()
	for _, b := range blocks {
		io.WriteString(sum, b.Plain())
		io.WriteString(sum, "\x00")
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// zipFile reads one member of an OOXML package, without unpacking the rest
// of it: the media inside a document can be far larger than its text.
func zipFile(data []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("ingest: %s has no %s", name, name)
}

// zipNames lists an OOXML package's members.
func zipNames(data []byte) ([]string, error) {
	r, err := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(r.File))
	for _, f := range r.File {
		out = append(out, f.Name)
	}
	return out, nil
}

// space collapses runs of whitespace, as paragraphs wrap in the source.
var space = regexp.MustCompile(`[ \t\x{00a0}\x{3000}]+`)

func collapse(s string) string {
	s = space.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
