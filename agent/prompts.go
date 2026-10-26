package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"prior/embed"
	"prior/ingest"
)

// PromptVersion names the prompts, which is recorded with every run so a
// change in wording is visible against the findings it produced.
const PromptVersion = "v2"

// Profile is what the analysis knows of a new document before it looks
// anything up: its shape, its modules and its terms.
type Profile struct {
	Title    string   `json:"title"`
	Module   string   `json:"module"`
	Headings []string `json:"headings"`
	Summary  string   `json:"summary"`
	Terms    []string `json:"terms"`
	Stats    Stats    `json:"stats"`
}

// Stats are counts the report shows.
type Stats struct {
	Sections int `json:"sections"`
	Chars    int `json:"chars"`
	Chunks   int `json:"chunks"`
	Tables   int `json:"tables"`
}

// FunctionPoint is one place in the product a document changes, as the
// model reads it out of the document.
type FunctionPoint struct {
	Module      string   `json:"module"`
	Function    string   `json:"function"`
	Topic       string   `json:"topic"`
	Rule        string   `json:"rule"`
	Quote       string   `json:"quote"`
	HeadingPath []string `json:"heading_path"`
	Impact      string   `json:"impact"`
}

// ProfileDocument reads a document's shape: its headings, its summary and
// the terms it uses, which the query generator searches with.
func ProfileDocument(doc *ingest.Doc, chunks []ingest.Chunk) Profile {
	p := Profile{Title: doc.Meta.Title, Module: doc.Meta.Module}
	var (
		summary strings.Builder
		tables  int
	)
	for _, b := range doc.Blocks {
		switch b.Kind {
		case ingest.KindHeading:
			p.Headings = append(p.Headings, b.Text)
			p.Stats.Sections++
		case ingest.KindTable:
			tables++
		}
	}
	for _, c := range chunks {
		if summary.Len() > 1200 {
			break
		}
		summary.WriteString(c.Content)
		summary.WriteString("\n")
	}
	p.Summary = strings.TrimSpace(summary.String())
	p.Terms = terms(doc, 40)
	p.Stats = Stats{Sections: p.Stats.Sections, Chars: doc.Chars, Chunks: len(chunks), Tables: tables}
	return p
}

// terms are the words a document repeats, which are what a search for what
// it talks about matches on: Chinese bigrams and Latin words, longest
// first, without the stop words every requirement document shares.
func terms(doc *ingest.Doc, limit int) []string {
	counts := map[string]int{}
	var text strings.Builder
	for _, b := range doc.Blocks {
		text.WriteString(b.Plain())
		text.WriteByte('\n')
	}
	runes := []rune(strings.ToLower(text.String()))
	for i := range runes {
		r := runes[i]
		if !cjk(r) {
			continue
		}
		if i+1 < len(runes) && cjk(runes[i+1]) {
			counts[string(runes[i:i+2])]++
		}
		if i+2 < len(runes) && cjk(runes[i+2]) {
			counts[string(runes[i:i+3])]++
		}
	}
	var list []termCount
	for term, n := range counts {
		if n < 3 || stopTerms[term] {
			continue
		}
		list = append(list, termCount{term, n})
	}
	sortKV(list)
	out := make([]string, 0, limit)
	for _, it := range list {
		if len(out) == limit {
			break
		}
		// A term inside a longer one already chosen adds nothing.
		covered := false
		for _, chosen := range out {
			if strings.Contains(chosen, it.term) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, it.term)
		}
	}
	return out
}

// stopTerms are the words every document in this corpus repeats, which
// would otherwise flood the queries.
var stopTerms = map[string]bool{
	"需求": true, "文档": true, "功能": true, "支持": true, "增加": true, "产品": true,
	"用户": true, "系统": true, "页面": true, "数据": true, "管理": true, "内容": true,
	"信息": true, "展示": true, "优化": true, "实现": true, "使用": true, "进行": true,
	"可以": true, "需要": true, "如下": true, "如下所示": true, "相关": true, "配置": true,
	"版本": true, "字段": true, "列表": true, "详情": true, "点击": true, "选择": true,
}

// termCount is a term and how often the document uses it.
type termCount struct {
	term string
	n    int
}

func sortKV(list []termCount) {
	for i := range list {
		for j := i + 1; j < len(list); j++ {
			if list[j].n > list[i].n || (list[j].n == list[i].n && len(list[j].term) > len(list[i].term)) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
}

func cjk(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf)
}

// ExtractionPrompt asks for a document's function points and their rules:
// the ledger's baseline. The answer is JSON, as every model call is.
const extractionPrompt = `你是资深产品经理。下面是一份产品需求文档的摘要与章节结构。
请抽出其中的**功能点**，每个功能点给出它在产品中的位置与它规定的规则。

什么算功能点（重要）：
- 文档里**每一条对界面、交互、流程、字段、文案、展示规则的要求**都算一个功能点，哪怕它写得零散：
  「某列表增加某字段」「按钮文案改为 X」「某页按某规则排序」「点击后跳转某处」「某端与某端保持一致」
  「按某条件筛选」——这些都算，不要因为它们不是「大功能」就不写。
- 只描述现状、背景、收益的段落不算。

要求：
1. 功能点用「模块 · 功能 · 规则主题」三段式定位：**模块取本文档所属系统**（用下面「模块：」给出的值，不要写别的文档的系统名），功能取本文档里的界面或流程名。
2. rule 写出该功能点当前规定的规则，必须是文档里能读到的原意，不得发挥；一句话，60 字以内。
3. quote 逐字摘录**本文档正文**的一段原文（10-80 字）来支撑该规则；引用必须能在本文档里找到，否则该功能点会被丢弃。
4. heading_path 给出该规则所在章节的层级标题数组。
5. impact 只在规则明显影响存量数据、下游系统或既有需求时填写，否则留空。
6. 最多 12 个功能点。真有内容就写满，宁可写 8 条小而具体的，也不要只写 2 条笼统的；
   确实读不出规则时才少给，绝不编造（编造会被引用核对拦下，等于白跑）。

只输出 JSON：
{"function_points":[{"module":"","function":"","topic":"","rule":"","quote":"","heading_path":[],"impact":""}]}`

// DocumentBrief is what a prompt needs of a document, which the analysis
// has after reading it and the ingest has just parsed it.
type DocumentBrief struct {
	Title   string
	Date    time.Time
	Module  string
	DocType string
}

// ExtractFunctionPoints asks the model for the function points of one part of
// a document: a long document is read in parts, and each part's points are
// the ones its own text states.
func ExtractFunctionPoints(ctx context.Context, em *embed.Client, doc DocumentBrief, part Part, llmModel string, temperature float64) ([]FunctionPoint, bool, error) {
	if llmModel == "" {
		return nil, false, fmt.Errorf("agent: 未设置语言模型")
	}
	var b strings.Builder
	b.WriteString("文档标题：")
	b.WriteString(doc.Title)
	b.WriteString("\n文档日期：")
	b.WriteString(doc.Date.Format("2006-01-02"))
	b.WriteString("\n模块：")
	b.WriteString(doc.Module)
	b.WriteString("\n文档类型：")
	b.WriteString(doc.DocType)
	if part.Total > 1 {
		fmt.Fprintf(&b, "\n这是全文的第 %d/%d 段，只写这一段里的功能点。", part.Index, part.Total)
	}
	b.WriteString("\n\n章节结构：\n")
	for i, h := range part.Headings {
		if i == 40 {
			break
		}
		b.WriteString("- ")
		b.WriteString(h)
		b.WriteString("\n")
	}
	b.WriteString("\n正文：\n")
	b.WriteString(part.Text)

	answer, err := em.Generate(ctx, extractionPrompt, b.String(), true, temperatureOr(temperature))
	if err != nil {
		return nil, false, err
	}
	points, truncated, err := decodeFunctionPoints(answer)
	if err != nil {
		return nil, truncated, err
	}
	// A cut answer is the caller's to report: the points that arrived are
	// still this document's points, and what was lost is said in the stage
	// message rather than written into the ledger as if it were a rule.
	filtered := points[:0]
	for _, p := range points {
		if strings.TrimSpace(p.Rule) == "" && strings.TrimSpace(p.Function) == "" {
			continue
		}
		if strings.TrimSpace(p.Rule) == "" {
			p.Rule = p.Function
		}
		filtered = append(filtered, p)
	}
	return filtered, truncated, nil
}

// decodeFunctionPoints reads the model's ledger answer. A point written as
// a bare sentence is still a point: it is kept as its own rule rather than
// thrown away with the whole ledger.
func decodeFunctionPoints(answer string) ([]FunctionPoint, bool, error) {
	points, truncated, err := decodeLoosePartial[FunctionPoint](answer, "function_points",
		func(text string) (FunctionPoint, bool) {
			if strings.TrimSpace(text) == "" {
				return FunctionPoint{}, false
			}
			return FunctionPoint{Function: text, Rule: text}, true
		})
	return points, truncated, err
}

// temperatureOr is a model call's temperature, or the pipeline's default.
func temperatureOr(temperature float64) float64 {
	if temperature <= 0 {
		return DefaultTemperature
	}
	return temperature
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// The analysis prompts. Every one answers with JSON, and every one is told
// to quote the document rather than to paraphrase it: a finding's value is
// that a reader can check it against the text.

const itemsPrompt = `你在读一份产品需求文档，请把其中的**需求条目**抽出来，供后续评审使用。

要求：
1. 一条需求 = 文档要求系统做的一件事。列表项、表格行、用户故事、编号标题下的段落都算，散文式的背景描述不算。
2. **只有一句话的需求也要算**：像「支持导出」「列表增加筛选」「点击跳转详情」这种短句、按钮文案或标题式要求，哪怕只有几个字、没有上下文，也要抽出来；不要因为短或看起来像标题就跳过。抽不出角色/价值时把 missing 标出来即可。
3. quote 逐字摘录原文（10-120 字），不得改写。短句本身不足 10 字时按原样摘录。
4. need 用一句话复述该需求（不超过 50 字）。
5. role / action / value 尽力从原文读出「谁、做什么、为了什么」；读不出就留空，不要编造。
6. missing 列出原文里缺的要素，取值只能来自：role、action、value、rule、boundary、data_caliber。
7. heading_path 给出该条目所在章节的层级标题数组。
8. 最多 20 条，按文档顺序。

只输出 JSON：{"items":[{"id":"1","quote":"","need":"","role":"","action":"","value":"","missing":[],"heading_path":[]}]}`

const oneLinerPrompt = `你在评审产品需求文档。下面是一份文档里抽出的需求条目。
请挑出**不符合需求文档规范**、需要产品补充的条目（典型：只有一句话、缺角色、缺动作、缺价值、缺验收标准），把它们写成评审发现。

要求：
1. 逐条判断，**不要只看「缺失」标记**：原文只有一句话、没有触发条件/边界/验收标准的，即使 S0 没有标缺失，也要报。
2. 只针对确实不合格的条目，合格的不要出现；没有不合格的就返回空数组。
3. title 是一句结论（不超过 40 字），例如「一句话需求：创作者上传需要二次确认」。
4. detail 说明缺什么、为什么无法据此实现（不超过 120 字）。
5. quote 逐字摘录该条目原文；原文很短就按原样摘录，不要补写。
6. question 是给产品的问题，必须具体（问清缺失的那一项，通过什么方式补齐）。
7. suggested_fix 给出改写示范：作为<角色>，我希望<动作>，以便<价值>；约束与验收：…
8. severity：缺动作/角色记 S2，只有表述问题记 S3；confidence 0 到 1。

只输出 JSON：{"findings":[{"type":"one_liner","subtype":"unstructured_requirement","severity":"S2","confidence":0.8,"title":"","detail":"","quote":"","heading_path":[],"evidence":[],"question":"","suggested_fix":"","impact":""}]}`

const ambiguityPrompt = `你在评审产品需求文档。请找出**不明确、会让开发或测试无法确定实现**的地方。

六类：语义歧义（同一个词指两件事）、隐含假设（默认读者知道但没写）、边界条件缺失（失败/超时/空数据/极值）、数据口径不清（统计范围、时间口径、单位）、兼容性缺失（老版本、老数据、其他端）、指代与范围歧义（「之前」「原来」「所有」「上述」没有明确对象或版本）。

要求：
1. 每条必须给出 question —— 一个产品能直接回答的问题；写不出具体问题的不要报。
2. quote 逐字摘录触发该问题的原文（10-120 字）。
3. title 是一句结论（≤40 字），detail 说明为什么无法确定（≤120 字）。
4. severity：导致实现方向不同的记 S1，细节缺失记 S2，表述问题记 S3。
5. 最多 15 条，宁缺毋滥。

只输出 JSON：{"findings":[{"type":"ambiguity","subtype":"semantic|implicit_assumption|missing_branch|data_caliber|compatibility|quantified_ambiguity|unresolved_reference|open_scope","severity":"S2","confidence":0.7,"title":"","detail":"","quote":"","heading_path":[],"evidence":[],"question":"","suggested_fix":"","impact":""}]}`

const gapPrompt = `你在做需求文档的**跨文档对照评审**。下面给出一份新需求文档的要点，以及从历史文档库检索到的相关片段。

请只找两类问题：
- C1 文档间冲突：新文档的规则与历史文档的规则相互矛盾，且新文档没有说明如何处理（默认 S1）。
- C2 覆盖缺口：历史文档中有明确规则，新文档在这一功能点上没有交代，需要确认是否仍然适用（默认 S2，措辞用「需确认」）。

不要报文档内部的前后不一致：那是另一次检查，重复报会让同一条问题出现两次。

要求：
1. **每条必须引用历史片段**：evidence 里给出 chunk_id（只能用它下面列出的 id）与该片段的原文 quote。
2. quote 给出新文档中对应的原文片段；heading_path 是它在文档中的章节。
3. question 是给产品的确认问题；同一条不要重复问同一件事。
4. 不要报告「话题相关但规则无关」的内容；证据必须能支撑冲突或缺口本身。
5. 最多 12 条，按严重度从高到低。

只输出 JSON：{"findings":[{"type":"logic_gap","subtype":"conflict|coverage_gap","severity":"S1","confidence":0.75,"title":"","detail":"","quote":"","heading_path":[],"evidence":[{"chunk_id":0,"quote":""}],"question":"","suggested_fix":"","impact":""}]}`

const consistencyPrompt = `你在做需求文档的**文内一致性评审**。下面是一份文档的章节、需求条目和正文（长文档可能节选）。

请只找**同一份文档内部前后不一致**的地方：
- 同一功能在不同章节给出不同规则、阈值、范围或默认值；
- 一处要求做、另一处要求不做（或缺省说「不支持」）；
- 版本号、日期、负责人、统计口径前后矛盾（含与变更日志对不上）；
- 同一术语在两处指不同对象，或同一对象用了两个名字；
- 流程前一步的输出与后一步的输入对不上（字段名、状态名、单位）。

要求：
1. quote 逐字摘录**第一处**原文；evidence[0].quote 逐字摘录**第二处**原文。两处都必须能在本文档找到，不要改写，不要只给一处。
2. 不需要 chunk_id；流水线会在本文档里定位这两句话。
3. title 一句话说清冲突（≤40 字），detail 说明两处分别怎么说、为什么不能同时成立（≤120 字）。
4. question 问产品以哪一处为准、另一处怎么改。
5. severity：导致实现方向相反或数据算错的记 S1，阈值/口径不一致记 S2，措辞或命名不一致记 S3。
6. 最多 10 条；没有前后矛盾就返回空数组，不要把「不同章节讲不同功能」当成冲突。

只输出 JSON：{"findings":[{"type":"logic_gap","subtype":"internal_conflict","severity":"S1","confidence":0.75,"title":"","detail":"","quote":"","heading_path":[],"evidence":[{"quote":""}],"question":"","suggested_fix":"","impact":""}]}`

const signalPrompt = `你在做需求文档的**变更识别与影响面分析**。下面是新文档中被规则粗筛出的变更语句，以及历史文档库中的相关片段。

要求：
1. 只确认真正的变更：新文档明确要求改、删、加规则的（「不再」「废弃」「统一改为」「由 A 改为 B」等）。粗筛出的句子如果其实不是变更，就不要报。
2. 每条 finding 说明：before（历史规则，引用历史片段时逐字摘录）、after（新规则，逐字摘录新文档原文）、impact（影响面，逐条检查五个维度：存量数据、接口兼容、灰度与回滚、下游通知、受影响历史需求；没交代的维度写进 question）。
3. evidence 引用历史片段（用下面列出的 chunk_id 与原文 quote）。
4. quote 是新文档中的变更原文；heading_path 是它的章节。
5. 同时给出台账字段：module（系统/产品模块）、function（界面或流程）、topic（规则主题，如「列表展示范围」）、kind（added/modified/removed/clarified）。
6. severity：引发全量改造或存量迁移的记 S0，明显规则冲突记 S1，其余 S2/S3。

只输出 JSON：{"findings":[{"type":"change_signal","subtype":"deleted_clause|added_constraint|modified_threshold|changed_rule","severity":"S0","confidence":0.8,"title":"","detail":"","quote":"","heading_path":[],"evidence":[{"chunk_id":0,"quote":""}],"question":"","suggested_fix":"","impact":"","module":"","function":"","topic":"","kind":"modified","before":"","after":""}]}`
