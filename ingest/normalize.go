package ingest

import (
	"regexp"
	"strconv"
	"strings"
)

// CleanText strips the markup the editors these documents come from leave in
// their text: Feishu and Word export runs of text as <span style="…">…</span>,
// sometimes inside a .docx text run, and a document read with that in it
// quotes markup to the model instead of the sentence the reader wrote. The
// same exports leave <br> for a line break and entities for the characters
// they stand for.
//
// The cleaning is idempotent: the read paths call it again for rows written
// before the parser did, so an old document reads as its author wrote it
// without being re-ingested.
func CleanText(s string) string {
	if s == "" {
		return ""
	}
	s = foldWidth(s)
	s = htmlBlock.ReplaceAllString(s, "")
	s = htmlComment.ReplaceAllString(s, "")
	s = brTag.ReplaceAllString(s, "\n")
	s = htmlTag.ReplaceAllString(s, "")
	s = htmlEntity.ReplaceAllStringFunc(s, unescapeEntity)
	// A tag that was escaped once (&lt;span&gt;) becomes a tag after the
	// entities are decoded; the second pass removes it.
	s = htmlTag.ReplaceAllString(s, "")
	s = invisible.ReplaceAllString(s, "")
	return s
}

var (
	// htmlTag is the markup the editors leave behind: a known HTML tag,
	// with or without attributes, or any tag carrying attributes. A bare
	// unknown tag is left alone, because "<token>" in an API header is the
	// document's words, not markup. The pattern needs a letter after "<", so
	// a sentence that compares numbers ("1 < 2") is left alone too.
	htmlTag = regexp.MustCompile(`</?(?:a|abbr|b|big|blockquote|br|center|code|dd|del|div|dl|dt|em|font|h[1-6]|hr|i|img|ins|li|mark|ol|o:p|p|pre|s|small|span|strike|strong|sub|sup|table|tbody|td|tfoot|th|thead|tr|u|ul)(?:\s[^<>]*)?/?>|</?[a-zA-Z][a-zA-Z0-9:-]*\s[^<>]*/?>`)
	// brTag is a line break the export wrote, which reads as a newline.
	brTag = regexp.MustCompile(`(?i)<br\s*/?>`)
	// htmlBlock is a style or script block: its tags and its content are the
	// exporter's, not the document's.
	htmlBlock = regexp.MustCompile(`(?is)<(?:script|style)\b[^>]*>.*?</(?:script|style)>`)
	// htmlComment is a comment the exporter left behind.
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	// htmlEntity is an escaped character the same exports leave behind.
	htmlEntity = regexp.MustCompile(`&(?:amp|lt|gt|quot|apos|nbsp|#\d+|#x[0-9a-fA-F]+);`)
	// invisible is the zero-width space and the bidi controls an export
	// leaves around Chinese text.
	invisible = regexp.MustCompile(`[\x{200b}-\x{200f}\x{202a}-\x{202e}\x{2060}\x{feff}]`)
)

// unescapeEntity is an entity as the character it stands for.
func unescapeEntity(entity string) string {
	switch entity {
	case "&amp;":
		return "&"
	case "&lt;":
		return "<"
	case "&gt;":
		return ">"
	case "&quot;":
		return `"`
	case "&apos;":
		return "'"
	case "&nbsp;":
		return " "
	}
	if strings.HasPrefix(entity, "&#x") || strings.HasPrefix(entity, "&#X") {
		if n, err := strconv.ParseInt(entity[3:len(entity)-1], 16, 32); err == nil {
			return string(rune(n))
		}
		return entity
	}
	if strings.HasPrefix(entity, "&#") {
		if n, err := strconv.ParseInt(entity[2:len(entity)-1], 10, 32); err == nil {
			return string(rune(n))
		}
	}
	return entity
}

// Normalize cleans what a document's own furniture adds, and keeps the
// structure the chunker and the analysis work from.
func Normalize(blocks []Block) []Block {
	out := make([]Block, 0, len(blocks))
	var prev string
	for _, b := range blocks {
		if b.Kind == KindTable {
			rows := normalizeRows(b.Rows)
			if len(rows) == 0 {
				continue
			}
			b.Rows = rows
			out = append(out, b)
			prev = ""
			continue
		}
		text := normText(b.Text)
		if text == "" || isFurniture(text) {
			continue
		}
		// A heading repeated right after itself is a running header the
		// exporter wrote twice, as a page header and a section start.
		if b.Kind == KindHeading && text == prev {
			continue
		}
		b.Text = text
		out = append(out, b)
		prev = text
	}
	return mergeHeadings(out)
}

// normalizeRows drops empty rows and columns the exporter left behind.
func normalizeRows(rows [][]string) [][]string {
	out := make([][]string, 0, len(rows))
	width := 0
	for _, row := range rows {
		cells := make([]string, 0, len(row))
		filled := false
		for _, cell := range row {
			cell = normText(cell)
			if cell != "" {
				filled = true
			}
			cells = append(cells, cell)
		}
		if !filled {
			continue
		}
		if len(cells) > width {
			width = len(cells)
		}
		out = append(out, cells)
	}
	// A row shorter than the widest keeps its columns aligned.
	for i, row := range out {
		for len(row) < width {
			out[i] = append(out[i], "")
			row = out[i]
		}
	}
	return out
}

// mergeHeadings joins a heading that stands alone on its line with the one
// written under it, as "1. DAM-AI应用" over "1.1 列表数据" stays apart but a
// bare number joins its title.
func mergeHeadings(blocks []Block) []Block {
	out := make([]Block, 0, len(blocks))
	for i, b := range blocks {
		if b.Kind == KindHeading && isBareNumber(b.Text) && i+1 < len(blocks) && blocks[i+1].Kind == KindHeading {
			joined := blocks[i+1]
			joined.Level = min(joined.Level, b.Level)
			joined.Text = b.Text + " " + joined.Text
			out = append(out, joined)
			blocks[i+1] = Block{Kind: KindParagraph, Text: "\x00"}
			continue
		}
		if b.Text == "\x00" {
			continue
		}
		out = append(out, b)
	}
	return out
}

// isBareNumber says whether a heading is only a number, as "3".
func isBareNumber(s string) bool {
	s = strings.Trim(s, ". 、")
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// normText is a text as the pipeline stores it: editor markup gone, control
// characters gone, full-width forms of ASCII folded, whitespace collapsed.
func normText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = CleanText(s)
	s = control.ReplaceAllString(s, "")
	s = collapse(s)
	return strings.TrimSpace(s)
}

var (
	control   = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)
	furniture = regexp.MustCompile(`^(第\s*\d+\s*页|\d+\s*/\s*\d+页?|Page\s+\d+(\s+of\s+\d+)?|[-—]+\s*\d+\s*[-—]+|\d{1,3})$`)
)

// isFurniture says whether a line is a page number or a running head the
// exporter wrote, which carries nothing for search.
func isFurniture(s string) bool {
	if furniture.MatchString(s) {
		return true
	}
	// A line of one repeated character is a rule, not text.
	if len([]rune(s)) > 3 {
		first := []rune(s)[0]
		same := true
		for _, r := range s {
			if r != first {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// foldWidth turns the full-width forms of ASCII into their narrow ones,
// so that a search for "V1.0.0" finds "Ｖ１．０．０" too.
func foldWidth(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\u3000':
			out.WriteRune(' ')
		case r >= '\uff01' && r <= '\uff5e':
			out.WriteRune(r - 0xfee0)
		case r == '\uff0e':
			out.WriteRune('.')
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
