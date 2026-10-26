package ingest

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Meta is what the pipeline knows of a document before it is indexed. The
// product team's documents carry it unevenly, so every field records
// whether it was read or inferred.
type Meta struct {
	Title string
	// Date is the document's own date, as the library sorts by it. Source
	// says where it came from, so a reader can tell a date written in the
	// document from one taken from its file name.
	Date       time.Time
	DateSource string
	Version    string
	Author     string
	// ProductLine and Module scope what a document is about; Module is the
	// requirement's own name, as the manifest lists it.
	ProductLine string
	Module      string
	DocType     string
	// Manifest fields carry what the download manifest knows, which is
	// more trustworthy than what a parser can read.
	ManifestTitle string
	ManifestLink  string
	Status        string
	Confidence    float64
}

// dateSources are the ways a date is found, best first.
const (
	dateFromManifest = "manifest"
	dateFromFileName = "file_name"
	dateFromField    = "field"
	// dateFromHeading is a date read from loose text, which is where a
	// document mentions the day it was written without naming a field.
	dateFromHeading = "text"
	dateFromLog     = "change_log"
	// dateFromNameShort is a date a file name carries without its century,
	// as "230604" or "0812".
	dateFromNameShort = "file_name_short"
	// dateFromMonth is a year and a month the document states, without a day.
	dateFromMonth = "month"
	// dateFromFile is no date the document states: the library shows the
	// file's own time as a hint and marks the date unknown.
	dateFromFile = "missing"
)

// ExtractMeta reads a document's metadata from its path and its blocks.
// Every date candidate carries a rank, and the best one wins: the team
// writes dates in several places at once, and the date a reader means is
// the one the document states about itself, not the first number in it.
//
// docProps/core.xml is not read: its dcterms:created is the day the file was
// exported, which for every exported document in this corpus is its export
// day (2026-10-15), never the day the document was written.
func ExtractMeta(path string, blocks []Block) Meta {
	meta := Meta{DocType: "unknown"}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	meta.Title, meta.Version = titleAndVersion(base)
	if looksLikeURL(base) {
		// A file the browser saved is named after its address; the document
		// itself names its subject in its first heading.
		if heading := firstHeading(blocks); heading != "" {
			meta.Title = heading
		}
	}
	meta.Module = moduleOf(base)
	meta.Author = authorOf(blocks)

	best := dateCandidate{}
	consider := func(c dateCandidate) { best = best.better(c) }

	// The file's own time is the last way a date is found, and it bounds the
	// dates the document states: a document mentions the day something is
	// planned for, and that is not the day it was written.
	fileTime, statErr := statTime(path)

	if date, ok := dateInText(base); ok {
		consider(dateCandidate{date, dateFromFileName, rankOf(dateFromFileName)})
	} else if date, ok, src := shortDateInName(base, path); ok {
		consider(dateCandidate{date, src, rankOf(src)})
	}
	if date, ok, src := dateInBlocks(blocks, meta.Version, fileTime); ok {
		consider(dateCandidate{date, src, rankOf(src)})
	}
	if statErr == nil {
		consider(dateCandidate{fileTime, dateFromFile, rankOf(dateFromFile)})
	}
	meta.Date, meta.DateSource = best.date, best.source
	meta.Confidence = best.confidence()
	meta.DocType = docTypeOf(blocks, base)
	return meta
}

// dateCandidate is a date found somewhere, with how much it is trusted.
type dateCandidate struct {
	date   time.Time
	source string
	rank   int
}

// rankFileTime and the ranks after it order the ways a date is found: the
// higher the rank, the more the date is trusted. A date read from prose is
// a weaker statement than the date a file name or the document's own field
// carries, which is why they are ranked apart.
const (
	rankFileTime  = 20
	rankMonthOnly = 30
	rankLooseText = 40
	rankNameShort = 50
	rankFileName  = 80
	rankField     = 90
	// rankChangeLog is the entry of a change log that belongs to this
	// document's own version, which is the day the document was finished.
	rankChangeLog = 95
	rankManifest  = 100
)

// rankOf is how much a date from a source is trusted.
func rankOf(source string) int {
	switch source {
	case dateFromManifest:
		return rankManifest
	case dateFromLog:
		return rankChangeLog
	case dateFromField:
		return rankField
	case dateFromFileName:
		return rankFileName
	case dateFromNameShort:
		return rankNameShort
	case dateFromHeading:
		return rankLooseText
	case dateFromMonth:
		return rankMonthOnly
	case dateFromFile:
		return rankFileTime
	}
	return rankLooseText
}

func (c dateCandidate) better(other dateCandidate) dateCandidate {
	if c.date.IsZero() {
		return other
	}
	if other.date.IsZero() || c.rank > other.rank {
		return c
	}
	return other
}

func (c dateCandidate) confidence() float64 {
	switch c.rank {
	case rankManifest, rankField:
		return 0.95
	case rankFileName, rankChangeLog:
		return 0.9
	case rankNameShort:
		return 0.5
	case rankLooseText:
		return 0.5
	case rankMonthOnly:
		return 0.3
	case rankFileTime:
		return 0.2
	}
	return 0.4
}

// shortDateInName reads the dates the corpus writes without a century: names
// as "0812…" (month and day) and as "230604…" (year, month and day, two
// digits each). A name without a year takes the year of the file itself.
func shortDateInName(base, path string) (time.Time, bool, string) {
	if m := compact6RE.FindStringSubmatch(base); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		if date, ok := makeDate(2000+year, month, day); ok {
			return date, true, dateFromNameShort
		}
	}
	if m := mmddRE.FindStringSubmatch(base); m != nil {
		year := time.Now().Year()
		if info, err := statTime(path); err == nil {
			year = info.Year()
		}
		month, _ := strconv.Atoi(m[1])
		day, _ := strconv.Atoi(m[2])
		if date, ok := makeDate(year, month, day); ok {
			return date, true, dateFromNameShort
		}
	}
	return time.Time{}, false, ""
}

var (
	compact6RE = regexp.MustCompile(`^\s*(\d{2})(\d{2})(\d{2})[^\d]`)
	mmddRE     = regexp.MustCompile(`^\s*(\d{2})(\d{2})[^\d]`)
)

// statTime is the file's own modification time, the last way a date is
// found when neither the name nor the document states one.
func statTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// looksLikeURL says whether a file name is an address a browser saved a
// page under, as the corpus holds one of.
func looksLikeURL(base string) bool {
	lower := strings.ToLower(base)
	if strings.HasPrefix(lower, "http") || strings.Contains(lower, "feishu.cn") {
		return true
	}
	// A name of underscores and dashes with no space is a slug, not a title.
	return !strings.Contains(base, " ") && strings.Count(base, "_") >= 3
}

// firstHeading is a document's first heading, which names it when its file
// name does not.
func firstHeading(blocks []Block) string {
	for i, b := range blocks {
		if i > 20 {
			break
		}
		if b.Kind == KindHeading && len([]rune(b.Text)) >= 4 && len([]rune(b.Text)) <= 60 {
			return b.Text
		}
	}
	return ""
}

// titleAndVersion split a file name into the document's title and the
// version it carries, as "20251120示例需求文档V1.0.0".
func titleAndVersion(base string) (title, version string) {
	s := base
	// A trailing download suffix, as the manifest adds ("_MCtidhcy").
	if i := strings.LastIndex(s, "_"); i > 0 && len(s)-i <= 12 && !strings.ContainsAny(s[i:], "，。 ") {
		if !strings.ContainsAny(s[i+1:], "0123456789一二三四五六七八九十") || len(s)-i == 9 {
			s = s[:i]
		}
	}
	if m := versionRE.FindStringSubmatch(s); m != nil {
		version = m[0]
		s = strings.TrimSpace(strings.Replace(s, version, "", 1))
	}
	s = strings.TrimSpace(strings.Trim(s, "_-— "))
	return s, strings.ToUpper(version)
}

// versionRE matches the version a document's name carries.
var versionRE = regexp.MustCompile(`(?i)\bv?\d+(\.\d+){1,3}\b`)

// moduleOf is the module a document belongs to, as its name states it
// between brackets or after a dash.
func moduleOf(base string) string {
	if m := bracketRE.FindStringSubmatch(base); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// authorOf reads the person who wrote or reviewed the document, from the
// fields the team's documents open with: in a paragraph, or in the cells
// of the information table they put first.
func authorOf(blocks []Block) string {
	for i, text := range firstTexts(blocks, 60) {
		if i > 60 {
			break
		}
		for _, key := range []string{"审核人", "撰写人", "作者", "负责人"} {
			if name, ok := fieldValue(text, key); ok && name != "" {
				return name
			}
		}
	}
	return ""
}

// firstTexts yields the text of the blocks the pipeline reads fields from:
// paragraphs as they are, and a table's cells, so that "创建日期" is found
// whether the team wrote it in a line or in a cell.
func firstTexts(blocks []Block, limit int) []string {
	var out []string
	for _, b := range blocks {
		if len(out) >= limit {
			break
		}
		switch b.Kind {
		case KindTable:
			for _, row := range b.Rows {
				for i, cell := range row {
					if cell == "" {
						continue
					}
					out = append(out, cell)
					// A field and its value in two cells read as one line.
					if i+1 < len(row) && !strings.ContainsAny(cell, "：:") && row[i+1] != "" && len([]rune(cell)) <= 8 {
						out = append(out, cell+"："+row[i+1])
					}
				}
			}
		case KindHeading:
			out = append(out, b.Text)
		default:
			out = append(out, b.Text)
		}
	}
	return out
}

// fieldValue reads "键：值" or "键: 值" from a line.
func fieldValue(line, key string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, key) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, key))
	rest = strings.TrimLeft(rest, "：: 　")
	return strings.TrimSpace(rest), true
}

// dateInBlocks is the date a document states about itself: the field it
// opens with, the entry of its change log that belongs to its own version,
// or the newest day its prose states. docVersion is what the document's
// name calls it and upper the file's own time, which bounds what the
// document may say about the day it was finished.
func dateInBlocks(blocks []Block, docVersion string, upper time.Time) (time.Time, bool, string) {
	best := dateCandidate{}
	consider := func(date time.Time, source string) {
		best = best.better(dateCandidate{date, source, rankOf(source)})
	}
	for _, text := range firstTexts(blocks, 60) {
		for _, key := range []string{"创建日期", "创建时间", "撰写日期", "文档日期", "更新日期", "日期"} {
			value, ok := fieldValue(text, key)
			if !ok {
				continue
			}
			if date, ok := dateInText(value); ok {
				consider(date, dateFromField)
			} else if date, ok := monthInText(value); ok {
				consider(date, dateFromMonth)
			}
		}
	}
	// A change log anywhere in the document, which some documents put after
	// their introduction: the entry for this document's version is the day it
	// was finished. An entry for a later version was written into another
	// document's copy of the log.
	for _, b := range blocks {
		if b.Kind != KindTable || !changeLogTable(b.Rows) {
			continue
		}
		if date, ok := changeLogDate(b.Rows, docVersion); ok {
			consider(date, dateFromLog)
		}
	}
	// A document that names no date usually states the day it was written in a
	// line. The newest date in the range a document's own date can fall in is
	// the one it was finished on; a January first is a template's example, and
	// is used only when the texts state nothing else.
	texts := firstTexts(blocks, 1<<30)
	if date, ok := newestDateInTexts(texts, upper); ok {
		consider(date, dateFromHeading)
	} else if date, ok := newestMonthInTexts(texts, upper); ok {
		consider(date, dateFromMonth)
	}
	if best.date.IsZero() {
		return time.Time{}, false, ""
	}
	return best.date, true, best.source
}

// earliestDocumentDate is the first day the corpus spans: a number before it
// read as a date is not a day a document was written on.
var earliestDocumentDate = time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)

// newestDateInTexts is the newest date the texts state, kept to the range a
// document's own date can fall in.
func newestDateInTexts(texts []string, upper time.Time) (time.Time, bool) {
	var newest, newYear time.Time
	for _, text := range texts {
		date, ok := dateInText(text)
		if !ok || date.Before(earliestDocumentDate) || (!upper.IsZero() && date.After(upper)) {
			continue
		}
		if date.Month() == time.January && date.Day() == 1 {
			if newYear.IsZero() {
				newYear = date
			}
			continue
		}
		if date.After(newest) {
			newest = date
		}
	}
	if newest.IsZero() {
		newest = newYear
	}
	return newest, !newest.IsZero()
}

// newestMonthInTexts is the newest year and month the texts state, for a
// document that names only that much.
func newestMonthInTexts(texts []string, upper time.Time) (time.Time, bool) {
	newest := time.Time{}
	for _, text := range texts {
		date, ok := monthInText(text)
		if !ok || date.Before(earliestDocumentDate) || (!upper.IsZero() && date.After(upper)) {
			continue
		}
		if date.After(newest) {
			newest = date
		}
	}
	return newest, !newest.IsZero()
}

// monthInText finds a year and a month without a day, as the team writes them:
// 2026.10, 2026-10, 2026年10月. A month without its day is read as its first.
func monthInText(s string) (time.Time, bool) {
	m := monthRE.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	return makeDate(year, month, 1)
}

// changeLogTable says whether a table's first row reads as a log of
// changes, by its headings.
func changeLogTable(rows [][]string) bool {
	for i, row := range rows {
		if i > 2 {
			break
		}
		joined := strings.Join(row, " ")
		for _, key := range []string{"变更日志", "更新记录", "修改记录", "变更记录"} {
			if strings.Contains(joined, key) {
				return true
			}
		}
		hasTime, hasVersion := false, false
		for _, cell := range row {
			switch strings.TrimSpace(cell) {
			case "时间", "日期", "变更时间", "更新时间", "修订时间", "修订日期":
				hasTime = true
			case "版本号", "版本":
				hasVersion = true
			}
		}
		if hasTime && hasVersion {
			return true
		}
	}
	return false
}

// changeLogDate is the date a table of changes states for this document's own
// version: the newest entry at or below it, which is the day it was finished.
// A row whose version cannot be read is skipped, and a document whose own
// version cannot be read has no row that can be tied to it — the log of a
// copy holds the entries of every version it went through. When no row names
// a version at all, the newest date of the table is all it can say.
func changeLogDate(rows [][]string, docVersion string) (time.Time, bool) {
	docRank := VersionRank(docVersion)
	newest, anyDate, versioned := time.Time{}, time.Time{}, false
	for _, row := range rows {
		rank := -1
		for _, cell := range row {
			if r := VersionRank(cell); r > rank {
				rank = r
			}
		}
		if rank >= 0 {
			versioned = true
		}
		for _, cell := range row {
			date, ok := dateInText(cell)
			if !ok {
				continue
			}
			if anyDate.IsZero() || date.After(anyDate) {
				anyDate = date
			}
			if rank < 0 || docRank < 0 || rank > docRank {
				continue
			}
			if newest.IsZero() || date.After(newest) {
				newest = date
			}
		}
	}
	if newest.IsZero() && !versioned {
		// Nothing in the table is tied to a version: the newest date it holds
		// is the only reading left.
		newest = anyDate
	}
	return newest, !newest.IsZero()
}

// dateInText finds a date written the ways the team writes them:
// 2025.11.20, 2025-11-20, 20251120, 2025/11/20, 2025年11月20日.
func dateInText(s string) (time.Time, bool) {
	if m := dateRE.FindStringSubmatch(s); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		if date, ok := makeDate(year, month, day); ok {
			return date, true
		}
	}
	if m := compactRE.FindStringSubmatch(s); m != nil {
		// 8 digits, as 20251120, when they stand as a whole word.
		year, _ := strconv.Atoi(m[1][:4])
		month, _ := strconv.Atoi(m[1][4:6])
		day, _ := strconv.Atoi(m[1][6:])
		if date, ok := makeDate(year, month, day); ok {
			return date, true
		}
	}
	if m := shortRE.FindStringSubmatch(s); m != nil {
		// Two digits a part, as the team writes years from 2020 on.
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		if date, ok := makeDate(2000+year, month, day); ok {
			return date, true
		}
	}
	return time.Time{}, false
}

var (
	// dateRE reads a date the ways the team writes it, and refuses one that a
	// longer number runs into: "2024.05.016" is a typo for the sixteenth of
	// May, not the first with a six after it.
	dateRE    = regexp.MustCompile(`(20\d{2})\s*[-./年]\s*(\d{1,2})\s*[-./月]\s*(\d{1,2})([^\d]|$)`)
	compactRE = regexp.MustCompile(`\b(20\d{6})\b`)
	shortRE   = regexp.MustCompile(`\b(\d{2})[.\-/](\d{1,2})[.\-/](\d{1,2})\b`)
	// monthRE reads a year and a month the team writes without a day. The
	// character after it must not be a digit, so that a version number or a
	// longer number is not read as one.
	monthRE   = regexp.MustCompile(`(20\d{2})\s*[-./年]\s*(\d{1,2})([^\d]|$)`)
	bracketRE = regexp.MustCompile(`[【\[]([^】\]]{2,20})[】\]]`)
)

// makeDate keeps a date to the range the corpus spans, so that a version
// number or a phone number is not read as a date.
func makeDate(year, month, day int) (time.Time, bool) {
	if year < 2015 || year > 2035 || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
	if date.Day() != day {
		return time.Time{}, false
	}
	return date, true
}

// docTypeOf names what a document is, from its title and its shape.
func docTypeOf(blocks []Block, base string) string {
	lower := strings.ToLower(base)
	switch {
	case strings.Contains(base, "需求文档") || strings.Contains(base, "产品需求") || strings.Contains(base, "需求"):
		return "requirement"
	case strings.Contains(base, "问题汇总") || strings.Contains(base, "问题清单"):
		return "issue_list"
	case strings.Contains(base, "优化建议") || strings.Contains(base, "建议"):
		return "suggestion"
	case strings.Contains(base, "使用帮助") || strings.Contains(base, "说明"):
		return "guide"
	}
	switch {
	case strings.HasSuffix(lower, ".xlsx"):
		return "table"
	case strings.Contains(base, "字段表"):
		return "table"
	}
	headings := 0
	for _, b := range blocks {
		if b.Kind == KindHeading {
			headings++
		}
	}
	if headings > 6 {
		return "requirement"
	}
	return "unknown"
}

// SortByDate orders documents newest first, which is how the library
// shows them.
func SortByDate(docs []*Doc) {
	sort.SliceStable(docs, func(i, j int) bool {
		return docs[i].Meta.Date.After(docs[j].Meta.Date)
	})
}
