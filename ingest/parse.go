package ingest

import (
	"encoding/xml"
	"regexp"
	"strings"
)

// The OOXML parts below are read with encoding/xml over the document part
// alone: a document's media is skipped, which matters when a package is
// hundreds of megabytes of images around a few pages of text.

// docxBody is the part of word/document.xml the pipeline needs.
type docxBody struct {
	Body struct {
		Items []docxItem `xml:",any"`
	} `xml:"body"`
}

type docxItem struct {
	XMLName xml.Name
	Style   docxStyle `xml:"pPr>pStyle"`
	Num     docxNum   `xml:"pPr>numPr"`
	Runs    []docxRun `xml:"r"`
	Rows    []docxRow `xml:"tr"`
	// Paragraphs inside a table cell, and tables inside a cell, arrive as
	// children of the cell.
	Inner []docxItem `xml:",any"`
}

type docxStyle struct {
	Val string `xml:"val,attr"`
}

type docxNum struct {
	Ilvl struct {
		Val string `xml:"val,attr"`
	} `xml:"ilvl"`
	NumID struct {
		Val string `xml:"val,attr"`
	} `xml:"numId"`
}

type docxRun struct {
	Texts []string   `xml:"t"`
	Tabs  []struct{} `xml:"tab"`
	Break []struct{} `xml:"br"`
}

type docxRow struct {
	Cells []docxCell `xml:"tc"`
}

type docxCell struct {
	Inner []docxItem `xml:",any"`
}

// parseDocx reads a Word document's blocks: its paragraphs, its headings
// by their style, its lists by their numbering, and its tables row by row.
func parseDocx(data []byte) ([]Block, error) {
	raw, err := zipFile(data, "word/document.xml")
	if err != nil {
		return nil, err
	}
	var body docxBody
	if err := xml.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	var blocks []Block
	for _, item := range body.Body.Items {
		blocks = append(blocks, docxBlocks(item, 0)...)
	}
	return blocks, nil
}

// docxBlocks walks one paragraph or table, and what a table cell holds.
func docxBlocks(item docxItem, depth int) []Block {
	if depth > 4 {
		return nil
	}
	var out []Block
	switch item.XMLName.Local {
	case "p":
		text := docxText(item)
		if text == "" {
			return nil
		}
		switch {
		case item.Style.Val != "" && item.Style.Val != "a" && isHeadingStyle(item.Style.Val):
			out = append(out, Block{Kind: KindHeading, Level: headingLevel(item.Style.Val), Text: text})
		case item.Num.NumID.Val != "":
			out = append(out, Block{Kind: KindListItem, Text: text, Ordered: true})
		default:
			out = append(out, Block{Kind: KindParagraph, Text: text})
		}
	case "tbl":
		var rows [][]string
		for _, row := range item.Rows {
			cells := make([]string, 0, len(row.Cells))
			for _, cell := range row.Cells {
				var parts []string
				for _, inner := range cell.Inner {
					for _, b := range docxBlocks(inner, depth+1) {
						parts = append(parts, b.Plain())
					}
				}
				cells = append(cells, collapse(strings.Join(parts, " ")))
			}
			rows = append(rows, cells)
		}
		if len(rows) > 0 {
			out = append(out, Block{Kind: KindTable, Rows: rows})
		}
	}
	return out
}

// docxText is a paragraph's text, with tabs and line breaks as spaces.
func docxText(item docxItem) string {
	var parts []string
	for _, run := range item.Runs {
		for _, t := range run.Texts {
			parts = append(parts, t)
		}
	}
	return collapse(strings.Join(parts, ""))
}

// isHeadingStyle says whether a paragraph's style is one of the heading
// levels the exporters write, as Feishu and Word do ("1", "Heading1",
// "标题 1").
func isHeadingStyle(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return true
	}
	for _, prefix := range []string{"heading", "title", "标题", "subtitle"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// headingLevel is a heading's depth, from its style.
func headingLevel(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		return int(s[0] - '0')
	}
	for i, prefix := range []string{"heading", "title", "标题", "subtitle"} {
		if strings.HasPrefix(s, prefix) {
			rest := strings.TrimSpace(strings.TrimPrefix(s, prefix))
			if rest != "" && rest[0] >= '1' && rest[0] <= '9' {
				return int(rest[0] - '0')
			}
			if i == 1 {
				return 1
			}
			return 2
		}
	}
	return 2
}

// xlsxSharedStrings is a workbook's string table.
type xlsxSharedStrings struct {
	Items []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

// xlsxSheet is a worksheet's rows.
type xlsxSheet struct {
	Rows []struct {
		Cells []struct {
			Type  string `xml:"t,attr"`
			Value string `xml:"v"`
			Text  string `xml:"is>t"`
			Ref   string `xml:"r,attr"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

// parseXlsx reads a workbook's sheets as tables, one block a sheet, with
// its name the heading. A workbook is data a product team maintains by
// hand, as field lists and evaluation tables are, so it is indexed whole.
func parseXlsx(data []byte) ([]Block, error) {
	shared, err := sharedStrings(data)
	if err != nil {
		return nil, err
	}
	names, _ := zipNames(data)
	var blocks []Block
	for _, name := range names {
		if !strings.HasPrefix(name, "xl/worksheets/sheet") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		raw, err := zipFile(data, name)
		if err != nil {
			continue
		}
		var sheet xlsxSheet
		if err := xml.Unmarshal(raw, &sheet); err != nil {
			continue
		}
		rows := make([][]string, 0, len(sheet.Rows))
		for _, row := range sheet.Rows {
			cells := make([]string, 0, len(row.Cells))
			for _, cell := range row.Cells {
				value := cell.Value
				if cell.Type == "s" {
					value = shared[atoi(value)]
				} else if cell.Text != "" {
					value = cell.Text
				}
				cells = append(cells, collapse(value))
			}
			rows = append(rows, cells)
		}
		if len(rows) == 0 {
			continue
		}
		sheetName := sheetLabel(data, name)
		blocks = append(blocks, Block{Kind: KindHeading, Level: 1, Text: sheetName})
		blocks = append(blocks, Block{Kind: KindTable, Rows: rows})
	}
	return blocks, nil
}

func sharedStrings(data []byte) ([]string, error) {
	raw, err := zipFile(data, "xl/sharedStrings.xml")
	if err != nil {
		// A workbook without strings holds numbers only.
		return nil, nil
	}
	var s xlsxSharedStrings
	if err := xml.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(s.Items))
	for _, item := range s.Items {
		text := item.Text
		if text == "" {
			var parts []string
			for _, r := range item.Runs {
				parts = append(parts, r.Text)
			}
			text = strings.Join(parts, "")
		}
		out = append(out, collapse(text))
	}
	return out, nil
}

// sheetLabel reads a sheet's name from the workbook's own listing, else
// its file name.
func sheetLabel(data []byte, name string) string {
	raw, err := zipFile(data, "xl/workbook.xml")
	if err != nil {
		return name
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(raw, &wb); err != nil || len(wb.Sheets) == 0 {
		return name
	}
	for i, sheet := range wb.Sheets {
		if strings.HasSuffix(name, "sheet"+itoa(i+1)+".xml") {
			return sheet.Name
		}
	}
	return wb.Sheets[0].Name
}

// parseMarkdown reads a Markdown document's blocks: its headings, its
// lists, its tables and its code.
func parseMarkdown(text string) []Block {
	var blocks []Block
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "#"):
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			blocks = append(blocks, Block{Kind: KindHeading, Level: level, Text: markdownInline(strings.TrimSpace(trimmed[level:]))})
		case strings.HasPrefix(trimmed, "```"):
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			blocks = append(blocks, Block{Kind: KindCode, Text: strings.Join(code, "\n")})
		case isTableRow(trimmed):
			var rows [][]string
			for ; i < len(lines) && isTableRow(strings.TrimSpace(lines[i])); i++ {
				row := splitTableRow(strings.TrimSpace(lines[i]))
				if isTableRule(row) {
					continue
				}
				rows = append(rows, row)
			}
			i--
			if len(rows) > 0 {
				blocks = append(blocks, Block{Kind: KindTable, Rows: rows})
			}
		case strings.HasPrefix(trimmed, "> "):
			blocks = append(blocks, Block{Kind: KindQuote, Text: markdownInline(strings.TrimSpace(trimmed[2:]))})
		case markdownList(trimmed):
			ordered, text := markdownListItem(trimmed)
			blocks = append(blocks, Block{Kind: KindListItem, Text: markdownInline(text), Ordered: ordered})
		default:
			blocks = append(blocks, Block{Kind: KindParagraph, Text: markdownInline(trimmed)})
		}
	}
	return blocks
}

// markdownList says whether a line starts a list item.
func markdownList(s string) bool {
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "+ ") {
		return true
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' '
}

func markdownListItem(s string) (ordered bool, text string) {
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "+ ") {
		return false, s[2:]
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(s) {
		return true, s[i+2:]
	}
	return false, s
}

func isTableRow(s string) bool {
	return strings.HasPrefix(s, "|") && strings.HasSuffix(s, "|") && len(s) > 2
}

func splitTableRow(s string) []string {
	parts := strings.Split(strings.Trim(s, "|"), "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		// A cell goes through the same clean-up as a sentence: an exported
		// table carries the editor's <span style="…"> runs in its cells as
		// well as in its paragraphs.
		out = append(out, markdownInline(p))
	}
	return out
}

func isTableRule(row []string) bool {
	if len(row) == 0 {
		return false
	}
	for _, cell := range row {
		if cell != "" && strings.Trim(cell, "-: ") != "" {
			return false
		}
	}
	return true
}

// markdownInline strips the markup that carries no meaning for search:
// images, links keep their text, emphasis and inline code lose their
// markers, and the editor's HTML runs go with the rest of the markup.
func markdownInline(s string) string {
	s = imageLink.ReplaceAllString(s, "")
	s = linkText.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("**", "", "__", "", "`", "", "~~", "").Replace(s)
	s = CleanText(s)
	return collapse(s)
}

// parseText reads a plain text file's blocks: headings by their prefix,
// as "一、" or "1.2", and the rest as paragraphs.
func parseText(text string) []Block {
	var blocks []Block
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if level, title := textHeading(trimmed); level > 0 {
			blocks = append(blocks, Block{Kind: KindHeading, Level: level, Text: title})
			continue
		}
		blocks = append(blocks, Block{Kind: KindParagraph, Text: collapse(trimmed)})
	}
	return blocks
}

// textHeading recognises the numbered headings a Chinese requirement
// document writes as "一、" or "1.2".
func textHeading(s string) (int, string) {
	if i := strings.Index(s, "、"); i == 1 && isChineseNumeral(s[:1]) {
		return 1, collapse(s[i+len("、"):])
	}
	level, dots := 0, 0
	for level < len(s) && s[level] >= '1' && s[level] <= '9' {
		level++
	}
	for i := level; i < len(s); i++ {
		switch {
		case s[i] == '.':
			dots++
		case s[i] == ' ' || s[i] == '\t' || strings.ContainsRune("、：:", rune(s[i])) || strings.Count(s[i:], "、") == len(s[i:]):
			if dots > 0 && len(s) > i+1 {
				return dots + 1, collapse(s[i+1:])
			}
			return 0, s
		default:
			return 0, s
		}
	}
	return 0, s
}

func isChineseNumeral(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return r >= '一' && r <= '十'
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var (
	imageLink = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkText  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)
