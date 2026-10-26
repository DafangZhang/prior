package ingest

import (
	"testing"
	"time"
)

// TestDateWeightsAreSeparate covers the rule that each way of finding a date
// is trusted on its own: a number in a line of prose does not overrule the
// day the document's own name carries. The document below is the one from the
// corpus that made this clear: its test account was read as the day the
// document was written.
func TestDateWeightsAreSeparate(t *testing.T) {
	blocks := []Block{
		{Kind: KindHeading, Text: "230604 数字资产系统在线课程需求文档1期1.2.0"},
		{Kind: KindHeading, Text: "二、 变更日志"},
		{Kind: KindTable, Rows: [][]string{
			{"时间", "版本号", "变更人", "变更章节", "主要变更内容"},
			{"240612", "1.1.0", "张婧怡", "1.2", "列表卡片按钮文案统一"},
			{"240625", "1.2.0", "张婧怡", "1.2", "补充目录课程展示说明"},
		}},
		{Kind: KindParagraph, Text: "教师账号：测试账号：20240502 密码：123456"},
	}
	meta := ExtractMeta("230604 数字资产系统在线课程需求文档1期1.1.0_SajQdw2m.docx", blocks)
	if got := dateOf(meta); got != "2023-06-04" {
		t.Errorf("date = %s; want 2023-06-04", got)
	}
	if meta.DateSource == dateFromHeading {
		t.Errorf("source = %q; a name's date should not lose to prose", meta.DateSource)
	}
}

// TestChangeLogDateTakesThisVersion covers the entry a change log states for
// the document's own version: a copy of the log in an older document holds a
// later version's entry, and that entry belongs to the other document.
func TestChangeLogDateTakesThisVersion(t *testing.T) {
	log := [][]string{
		{"时间", "版本号", "变更人", "主要变更内容"},
		{"2025.01.02", "V1.0.0", "林知远", "创建"},
		{"2025.01.03", "V1.1.0", "林知远", "pc页面增加两个模块"},
		{"2025.01.06", "V1.1.0", "林知远", "H5页面修改需求"},
	}
	cases := []struct {
		version string
		want    string
	}{
		{"V1.0.0", "2025-01-02"},
		{"V1.1.0", "2025-01-06"},
	}
	for _, c := range cases {
		date, ok := changeLogDate(log, c.version)
		if !ok || date.Format("2006-01-02") != c.want {
			t.Errorf("changeLogDate(%s) = %v, %v; want %s", c.version, date, ok, c.want)
		}
	}
	// A document whose name carries no version has no row that can be tied to
	// it, and the log says nothing about its date.
	if date, ok := changeLogDate(log, ""); ok {
		t.Errorf("changeLogDate(\"\") = %v; want no date", date)
	}
}

// TestChangeLogDateWithoutVersions covers a log whose cells name no version:
// the newest date in it is the only reading left.
func TestChangeLogDateWithoutVersions(t *testing.T) {
	rows := [][]string{
		{"时间", "版本号", "主要变更内容"},
		{"2023.11.01", "初版", "创建"},
		{"2023.11.09", "—", "补充说明"},
	}
	date, ok := changeLogDate(rows, "V1.0.0")
	if !ok || date.Format("2006-01-02") != "2023-11-09" {
		t.Errorf("changeLogDate = %v, %v; want 2023-11-09", date, ok)
	}
}

// TestVersionPairDatesAreOrdered covers two versions of one document: each
// takes the date its own change log states, so the newer reads after the
// older even when their version tables both kept the older date.
func TestVersionPairDatesAreOrdered(t *testing.T) {
	versionTable := func() []Block {
		return []Block{{Kind: KindTable, Rows: [][]string{
			{"版本号:V1.0.0", "创建日期:2023.09.20", "审核人:周明"},
		}}}
	}
	header := []string{"时间", "版本号", "变更人", "主要变更内容"}
	older := append(versionTable(), Block{Kind: KindTable, Rows: append([][]string{header},
		[]string{"2023.10.26", "1.0.1", "林知远", "修改1.4软件列表描述和链接"})})
	newer := append(versionTable(), Block{Kind: KindTable, Rows: append([][]string{header},
		[]string{"2023.10.26", "1.0.1", "林知远", "修改1.4软件列表描述和链接"},
		[]string{"2023.10.26", "1.1.0", "林知远", "修改1.3卡片字段"},
		[]string{"2023.11.14", "1.1.1", "林知远", "调整顺序"})})

	first := ExtractMeta("SaaS 官网自适应-软件列表、软件详情需求文档V1.0.0_WhKydb3u.docx", older)
	second := ExtractMeta("SaaS 官网自适应-软件列表、软件详情需求文档V1.1.0_Gbotdpye.docx", newer)
	if got := dateOf(first); got != "2023-09-20" {
		t.Errorf("V1.0.0 date = %s; want 2023-09-20", got)
	}
	if got := dateOf(second); got != "2023-10-26" {
		t.Errorf("V1.1.0 date = %s; want 2023-10-26", got)
	}
	if !first.Date.Before(second.Date) {
		t.Errorf("the older version reads after the newer: %s, %s", first.Date, second.Date)
	}
}

// TestRevisionLogIsAChangeLog covers the second shape of a change log the team
// writes, whose time column is called 修订时间. Its rows are dated with the
// version they belong to, and the newest row of this document's own version is
// the day it was finished.
func TestRevisionLogIsAChangeLog(t *testing.T) {
	blocks := []Block{
		{Kind: KindTable, Rows: [][]string{
			{"文档版本号:", "V1.0.0", "文档编号:", "001"},
			{"编写人:", "林知远", "编写日期:", "2023.08.21"},
		}},
		{Kind: KindHeading, Text: "修订记录:"},
		{Kind: KindTable, Rows: [][]string{
			{"版本", "修订时间", "修订人", "修订类型", "修订章节", "内容"},
			{"v1.0.0", "林知远", "2023.08.21", "A", "全部", "全部"},
			{"V1.0.1", "林知远", "2023.09.22", "M", "2.3.3", "新增创作者列表"},
		}},
	}
	meta := ExtractMeta("SaaS 官网自适应-专区详情、素材详情等需求文档V1.0.0_YYBSdtaE.docx", blocks)
	if got := dateOf(meta); got != "2023-08-21" {
		t.Errorf("date = %s; want 2023-08-21, the entry of its own version", got)
	}
	if meta.DateSource != dateFromLog {
		t.Errorf("source = %q; want %q", meta.DateSource, dateFromLog)
	}
}

// TestMonthOnlyDate covers a document that states a year and a month without a
// day: the first of that month stands for it, and it is weaker than a day.
func TestMonthOnlyDate(t *testing.T) {
	blocks := []Block{{Kind: KindTable, Rows: [][]string{
		{"版本号:V1.0.0", "创建日期:2026.05", "审核人:周明"},
	}}}
	meta := ExtractMeta("企业内容平台首页开发需求文档.docx", blocks)
	if got := dateOf(meta); got != "2026-05-01" {
		t.Errorf("date = %s; want 2026-05-01", got)
	}
	if meta.DateSource != dateFromMonth {
		t.Errorf("source = %q; want %q", meta.DateSource, dateFromMonth)
	}
	if date, ok := monthInText("2026年10月"); !ok || date.Format("2006-01-02") != "2026-10-01" {
		t.Errorf("monthInText(2026年10月) = %v, %v", date, ok)
	}
	if date, ok := dateInText("2024.05.016"); ok {
		t.Errorf("dateInText read a typo as a date: %v", date)
	}
}

// TestNoDateIsUnknown covers a document that states no date at all: nothing is
// guessed from what happens to be in it, and the date is left unknown.
func TestNoDateIsUnknown(t *testing.T) {
	blocks := []Block{
		{Kind: KindHeading, Text: "综合搜索方案"},
		{Kind: KindParagraph, Text: "本版本需求范围：搜索结果页增加专区内容。"},
	}
	meta := ExtractMeta("综合搜索方案_DqOtdc4m.docx", blocks)
	if !meta.Date.IsZero() {
		t.Errorf("date = %s; want none", meta.Date)
	}
	if meta.DateSource == dateFromManifest || meta.DateSource == dateFromLog {
		t.Errorf("source = %q; want no date the document states", meta.DateSource)
	}
}

// TestProseDateIsTheNewest covers a document whose date is stated in its prose
// only: the newest day it names is the one it was finished on.
func TestProseDateIsTheNewest(t *testing.T) {
	blocks := []Block{
		{Kind: KindHeading, Text: "网站顶栏菜单调整"},
		{Kind: KindParagraph, Text: "2023.12.18 评审通过"},
		{Kind: KindParagraph, Text: "初稿写于 2023.12.05"},
	}
	meta := ExtractMeta("网站顶栏菜单调整_PPjCduSr.docx", blocks)
	if got := dateOf(meta); got != "2023-12-18" {
		t.Errorf("date = %s; want 2023-12-18", got)
	}
	if meta.DateSource != dateFromHeading {
		t.Errorf("source = %q; want %q", meta.DateSource, dateFromHeading)
	}
}

// dateOf is a date as the library shows it.
func dateOf(meta Meta) string {
	if meta.Date.IsZero() {
		return ""
	}
	return meta.Date.Format("2006-01-02")
}

// TestSortByDate covers the library's order, newest first.
func TestSortByDate(t *testing.T) {
	docs := []*Doc{
		{Meta: Meta{Title: "b", Date: time.Date(2023, 9, 20, 0, 0, 0, 0, time.Local)}},
		{Meta: Meta{Title: "a", Date: time.Date(2023, 10, 26, 0, 0, 0, 0, time.Local)}},
	}
	SortByDate(docs)
	if docs[0].Meta.Title != "a" || docs[1].Meta.Title != "b" {
		t.Errorf("order = %s, %s", docs[0].Meta.Title, docs[1].Meta.Title)
	}
}
