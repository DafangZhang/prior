package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"prior/embed"
	"prior/ingest"
	"prior/retrieval"
	"prior/store"
)

// DefaultTemperature is what the model calls run at when nothing says
// otherwise: the pipeline's answers are extracted rather than written, and a
// low temperature keeps a run reproducible.
const DefaultTemperature = 0.1

// AnalyzeOptions is what a review runs with.
type AnalyzeOptions struct {
	// Doc is the new document, already stored.
	Doc store.Doc
	// Chunks are its chunks, which the findings quote and cite.
	Chunks []store.Chunk
	// Profile is what the document's own reading produced.
	Profile Profile
	// Retrieval numbers; zero takes the pipeline's defaults.
	Search retrieval.Options
	// ParserVersion and PromptVersion are recorded with the run.
	PromptVersion string
	// Progress receives the stages; it may be nil.
	Progress func(Event)
	// EmbedModel and LLMModel are recorded with the run.
	EmbedModel, LLMModel string
	// Temperature is what the model calls run at; zero takes the default.
	Temperature float64
}

// AnalyzeResult is what a review found.
type AnalyzeResult struct {
	RunID    int64
	Findings []store.Finding
	// PreReview is the verdict the review draws on the document itself: the
	// admission level, the risks behind it, and the questions it asks.
	PreReview store.PreReview
	Records   int
	Points    int
	Coverage  retrieval.Coverage
	Docs      int
	Degraded  []string
}

// Analyze reviews a new document against the knowledge base: it profiles
// the document, checks it against the rules, compares it with the history
// the retrieval finds, and writes what it found, together with the change
// records that keep the ledger of each function point.
func Analyze(ctx context.Context, st *store.Store, em *embed.Client, opts AnalyzeOptions) (AnalyzeResult, error) {
	var res AnalyzeResult
	if opts.Progress == nil {
		opts.Progress = func(Event) {}
	}
	if opts.PromptVersion == "" {
		opts.PromptVersion = PromptVersion
	}
	search := opts.Search
	if search.MaxQueries == 0 {
		search = retrieval.Defaults()
	}

	runID, err := st.StartRun(ctx, opts.Doc.ID, opts.Doc.Title, map[string]any{
		"embed": opts.EmbedModel, "llm": opts.LLMModel, "prompt": opts.PromptVersion,
		"temperature": 0.1,
	})
	if err != nil {
		return res, err
	}
	res.RunID = runID
	stage := func(name, message string) {
		st.SetRunStage(ctx, runID, name, map[string]any{"stage": name, "message": message})
		opts.Progress(Event{Stage: name, Message: message})
	}

	// S0：文档画像与需求条目
	stage("S0", "解析文档结构：章节 "+itoa(len(opts.Profile.Headings))+"，术语 "+itoa(len(opts.Profile.Terms)))
	items, itemsErr := extractRequirementItems(ctx, em, opts)
	if itemsErr != nil {
		res.Degraded = append(res.Degraded, "S0 需求条目抽取失败："+itemsErr.Error())
	}

	// 检索：历史对照的上下文
	stage("R", "检索历史文档")
	found, err := retrieval.Search(ctx, st, em, retrieval.Input{
		Title: opts.Doc.Title, Module: opts.Doc.Module,
		Headings: opts.Profile.Headings, Terms: opts.Profile.Terms, Summary: opts.Profile.Summary,
	}, search)
	if err != nil {
		return res, err
	}
	res.Coverage, res.Docs = found.Coverage, len(found.Docs)
	stage("R", fmt.Sprintf("查询 %d 条，融合 %d 块，重排保留 %d，文档 %d 篇",
		found.Coverage.Queries, found.Coverage.Candidates, found.Coverage.Kept, len(found.Docs)))
	history := historyText(found.Docs)
	refs := evidenceRefs(found.Docs)

	// S1：一句话需求
	stage("S1", "规范一句话需求")
	var findings []store.Finding
	modelOnes, onesErr := oneLiners(ctx, em, opts, items)
	if onesErr != nil {
		res.Degraded = append(res.Degraded, "S1 一句话需求失败："+onesErr.Error())
	}
	findings = append(findings, modelOnes...)
	// The model only sees the items S0 extracted. A requirement written as a
	// single line is exactly the kind S0 tends to skip, so the rule layer
	// reads the document itself and reports the short, qualifier-less ones.
	ruleOnes := oneLinerFindings(opts.Chunks)
	findings = append(findings, ruleOnes...)
	stage("S1", fmt.Sprintf("模型 %d 条，规则层 %d 条", len(modelOnes), len(ruleOnes)))

	// S2：明确性检查（规则层 + 规范性 + 模型层）
	stage("S2", "明确性检查")
	ruleFindings := ruleLayer(opts.Doc, opts.Chunks)
	findings = append(findings, ruleFindings...)
	// The document is also judged as a document: the sections a requirement
	// document has to have, which is a different question from whether what
	// it says is clear.
	spec := specFindings(opts.Chunks, docHeadings(opts.Doc, opts.Chunks))
	findings = append(findings, spec...)
	stage("S2", fmt.Sprintf("规则层 %d 条，规范性 %d 条", len(ruleFindings), len(spec)))
	if llm, err := llmAmbiguities(ctx, em, opts); err != nil {
		res.Degraded = append(res.Degraded, "S2 模型层检查失败："+err.Error())
	} else {
		findings = append(findings, llm...)
	}

	// P：预评审（文档本身够不够开工）
	stage("P", "预评审：准入定级与工程缺口")
	pre := preReviewOf(ctx, em, opts, items, itemsErr, findings)
	findings = append(findings, pre.Findings...)
	res.PreReview = pre.Review
	stage("P", fmt.Sprintf("%s：%s", verdictLabel(pre.Review.Verdict), pre.Review.Summary))
	if err := st.SavePreReview(ctx, runID, pre.Review); err != nil {
		res.Degraded = append(res.Degraded, "P 预评审结论未能写入："+err.Error())
	}

	// S3：逻辑漏洞（与历史对照 + 文内前后不一致）
	stage("S3", "对照历史与文内找逻辑漏洞")
	var gaps, inside []store.Finding
	if len(items) == 0 {
		res.Degraded = append(res.Degraded, "S3 跨文档对照跳过：S0 没有读出需求条目")
	} else if got, err := logicGaps(ctx, st, em, opts, items, history, refs); err != nil {
		res.Degraded = append(res.Degraded, "S3 跨文档对照失败："+err.Error())
	} else {
		gaps = got
	}
	if got, err := internalConflicts(ctx, em, opts, items); err != nil {
		res.Degraded = append(res.Degraded, "S3 文内一致性检查失败："+err.Error())
	} else {
		inside = got
	}
	findings = append(findings, gaps...)
	findings = append(findings, inside...)
	stage("S3", fmt.Sprintf("跨文档 %d 条，文内不一致 %d 条", len(gaps), len(inside)))

	// S4：变更信号与功能点变更记录
	stage("S4", "识别变更信号并记账")
	signals, records, points, err := changeSignals(ctx, st, em, opts, history, found, runID)
	if err != nil {
		res.Degraded = append(res.Degraded, "S4 变更信号失败："+err.Error())
	}
	findings = append(findings, signals...)
	res.Records, res.Points = records, points

	// S4b：功能点与变更记录。信号只覆盖「读起来像改动」的句子，而账本要
	// 覆盖新文档涉及的每个功能点：新的记为 baseline，与库中规则不同的记为
	// modified，历史规则的原文放进 before，随后可被人工确认。
	stage("S4", "抽取功能点并记账")
	ledgerRecords, ledgerPoints, conflicts, err := writeLedger(ctx, st, em, opts, runID)
	if err != nil {
		res.Degraded = append(res.Degraded, "S4 功能点记账失败："+err.Error())
	}
	res.Records += ledgerRecords
	res.Points += ledgerPoints
	// A change the document leaves unresolved is a finding of its own: the
	// ledger says what changed, and this says what has to be answered.
	findings = append(findings, conflicts...)

	// 兜底与约束：所有发现必带原文片段；逻辑漏洞与变更信号必带历史证据
	findings = dedupeFindings(findings)
	findings = enforce(findings, opts.Chunks)
	// A gap without history is downgraded to an ambiguity above; two such
	// downgrades can become the same finding.
	findings = dedupeFindings(findings)
	res.Findings = findings

	// S5：汇编落库
	stage("S5", fmt.Sprintf("汇编 %d 条发现", len(findings)))
	if err := st.SaveFindings(ctx, runID, findings); err != nil {
		return res, err
	}
	status := "succeeded"
	if len(res.Degraded) > 0 {
		status = "degraded"
	}
	if err := st.FinishRun(ctx, runID, status, "S5", strings.Join(res.Degraded, "；")); err != nil {
		return res, err
	}
	stage("S5", "完成")
	return res, nil
}

// temperature is what this run's model calls use.
func (o AnalyzeOptions) temperature() float64 {
	if o.Temperature <= 0 {
		return DefaultTemperature
	}
	return o.Temperature
}

// DatedBy says whether a document states a date of its own: a document whose
// date is unknown is not placed on the timeline, and its entries are read in
// the order they were recorded.
func DatedBy(doc store.Doc) bool {
	return !doc.Date.IsZero() && doc.DateSource != "" && doc.DateSource != "missing"
}

// writeLedger extracts a document's function points — a part at a time, since
// a long document's rules are not all in its opening — and writes each into
// its own ledger: a new function point's first entry is its baseline, and one
// the knowledge base already holds gets an entry that says what changed, with
// the rule it held before.
func writeLedger(ctx context.Context, st *store.Store, em *embed.Client, opts AnalyzeOptions, runID int64) (int, int, []store.Finding, error) {
	brief := DocumentBrief{
		Title: opts.Doc.Title, Date: opts.Doc.Date, Module: opts.Doc.Module, DocType: opts.Doc.DocType,
	}
	var (
		points   []FunctionPoint
		cut      bool
		firstErr error
	)
	for _, part := range parts(opts.Chunks, opts.Profile.Headings) {
		notePart(opts, "S4", part)
		got, truncated, err := ExtractFunctionPoints(ctx, em, brief, part, opts.LLMModel, opts.temperature())
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("第 %d/%d 段：%w", part.Index, part.Total, err)
			}
			continue
		}
		points, cut = append(points, got...), cut || truncated
	}
	if len(points) == 0 && firstErr != nil {
		return 0, 0, nil, firstErr
	}
	// The points already in the ledger are what a document's points are
	// identified against, so that the same rule restated in another
	// document's words becomes an entry on the same point rather than a
	// second baseline.
	registry, err := st.FunctionPoints(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	records, candidates, dropped, seen := 0, 0, 0, map[int64]bool{}
	// conflicts are the changes the document leaves unresolved: they are the
	// review's own findings, returned with the ledger's counts.
	var conflicts []store.Finding
	// The same rule can be stated in two sections, and then two parts both
	// read it: the second is the same entry, not a change to the first.
	written := map[int64]string{}
	for _, p := range points {
		module := firstNonEmpty(p.Module, opts.Doc.Module, "未归类")
		function := firstNonEmpty(p.Function, "未命名功能")
		label := labelOfPoint(p, opts.Doc.Module)
		match := resolvePoint(module, function, p.Topic, label, p.Rule, registry, IdentityThreshold)
		target := store.FunctionPoint{Module: module, Function: function, Topic: p.Topic, Label: label}
		if match.Kind == "exact" || match.Kind == "normalized" {
			// Write onto the point the ledger already has, under its own
			// name, so the group does not split.
			target = match.Point
		}
		pointID, err := st.UpsertFunctionPoint(ctx, target, opts.Doc.ID)
		if err != nil {
			return records, len(seen), conflicts, err
		}
		if rule := strings.TrimSpace(p.Rule); written[pointID] == rule {
			continue
		}
		written[pointID] = strings.TrimSpace(p.Rule)
		if match.Kind == "candidate" && match.Point.ID != pointID {
			candidates++
		}
		kind, before := "baseline", ""
		if prev, ok, err := st.LatestRecordBefore(ctx, pointID, opts.Doc.Date, DatedBy(opts.Doc)); err == nil && ok {
			before = prev.After
			switch {
			case strings.TrimSpace(before) == strings.TrimSpace(p.Rule):
				kind = "clarified"
			case before == "":
				kind = "baseline"
			default:
				kind = "modified"
			}
		}
		evidence := []store.Evidence{}
		status := "extracted"
		if p.Quote != "" {
			if !inDocument(opts.Chunks, p.Quote) {
				// A rule the model wrote itself rather than quoted: the
				// ledger still records it — tracking a rule across versions
				// is what the ledger is for — and the row says its quote
				// could not be verified, so a reader knows to check it.
				dropped++
				status = "unquoted"
			} else {
				evidence = append(evidence, store.Evidence{
					DocID: opts.Doc.ID, DocTitle: opts.Doc.Title, Quote: p.Quote,
					Path: p.HeadingPath, Date: opts.Doc.Date.Format("2006-01-02"),
				})
			}
		}
		record := store.ChangeRecord{
			PointID: pointID, DocID: opts.Doc.ID, RunID: runID, Kind: kind,
			Before: before, After: p.Rule, Impact: p.Impact, Evidence: evidence,
			Status: status, Recorded: opts.Doc.Date,
			MatchKind: matchKindOf(match, pointID),
		}
		// A rule that changed against the ledger is a conflict until the
		// document says what happens to what the old rule governed: a
		// "modified" entry the document does not resolve is the question the
		// review has to ask, and the old rule it replaced is the evidence.
		if kind == "modified" {
			if q, ok := unresolvedChange(before, p.Rule, opts.Chunks); ok {
				record.Kind, record.Status = "conflicted", "unresolved"
				record.Impact = strings.TrimSpace(record.Impact + "\n" + q)
				// The finding cites the old rule as history, which is what a
				// logic gap has to carry: the ledger's own entry is the
				// evidence, with the chunk it came from, so the constraint
				// does not read it as an unverifiable quote.
				old := store.Evidence{Quote: before}
				if chunk, ok := locateQuote(opts.Chunks, before); ok {
					old.ChunkID, old.DocID, old.DocTitle, old.Path = chunk.ID, opts.Doc.ID, opts.Doc.Title, chunk.Path
				} else if prev, ok, err := st.LatestRecordBefore(ctx, pointID, opts.Doc.Date, DatedBy(opts.Doc)); err == nil && ok {
					old.DocID, old.DocTitle = prev.DocID, prev.DocTitle
					if old.DocTitle == "" {
						old.DocTitle = "历史台账"
					}
					old.Date = prev.DocDate.Format("2006-01-02")
					if len(prev.Evidence) > 0 {
						old.ChunkID = prev.Evidence[0].ChunkID
						old.Path = prev.Evidence[0].Path
					}
				}
				if old.ChunkID == 0 {
					// No chunk to point at: the ledger's own text is the
					// evidence, and the row says so rather than passing it
					// off as a quote from a document.
					old.Quote = "历史台账：" + truncate(before, 120)
				}
				conflicts = append(conflicts, store.Finding{
					Type: "logic_gap", Subtype: "unresolved_conflict", Severity: "S1", Confidence: 0.7,
					Title: "新规则与历史规则冲突，且未写明如何处置",
					Detail: "这条规则在历史里是「" + truncate(before, 120) + "」，本文档改为「" +
						truncate(p.Rule, 120) + "」，但没有交代存量数据或旧行为怎么办。",
					Span:     store.Span{Quote: p.Quote, Path: p.HeadingPath},
					Evidence: append([]store.Evidence{old}, evidence...),
					Question: "旧规则下的存量数据/已生效的配置如何处理？是刷数、兼容一段时间，还是直接按新规则？",
					Status:   "open",
				})
			}
		}
		if _, err := st.SaveChangeRecord(ctx, record); err != nil {
			return records, len(seen), conflicts, err
		}
		// A point counts as this run's once it has a record: one whose quote
		// was not in the document is not a function point of this run.
		seen[pointID] = true
		records++
	}
	// Where the ledger went is part of reading a run: a document whose
	// points the model did not state, or stated with quotes from elsewhere,
	// must not look like a document with no function points.
	message := fmt.Sprintf("台账 %d 条 / 功能点 %d 个（抽取 %d 个）", records, len(seen), len(points))
	if candidates > 0 {
		message += fmt.Sprintf("，%d 条疑似与已有功能点重复（待确认合并）", candidates)
	}
	if dropped > 0 {
		message += fmt.Sprintf("，%d 个功能点的引用未在本文档找到（记为「无引用」，需人工核对）", dropped)
	}
	if len(points) == 0 {
		message += "，模型没有给出功能点"
	}
	if cut {
		message += "，模型回答达到长度上限（已按完整条目保留）"
	}
	if firstErr != nil {
		message += "；有段落没读成：" + firstErr.Error()
	}
	if len(conflicts) > 0 {
		message += fmt.Sprintf("，%d 条改动未写明旧规则如何处置", len(conflicts))
	}
	opts.progress(Event{Stage: "S4", Message: message})
	return records, len(seen), conflicts, nil
}

// docHeadings is the document's own section titles, which the spec check
// reads for the sections it expects.
func docHeadings(doc store.Doc, chunks []store.Chunk) []string {
	out := make([]string, 0, 32)
	for _, chunk := range chunks {
		out = append(out, chunk.Path...)
	}
	return out
}

// matchKindOf says how a record's point was identified: a point the ledger
// already had, a candidate a person should look at, or a new one.
func matchKindOf(match pointMatch, pointID int64) string {
	switch match.Kind {
	case "exact", "normalized":
		if match.Point.ID == pointID {
			return match.Kind
		}
	case "candidate":
		if match.Point.ID != pointID {
			return "candidate"
		}
	}
	return "new"
}

// Prompt budgets. A review's model may be a local one with an 8k context, and
// the gap, signal and consistency prompts carry history, the document's items
// and a citation index. These bounds keep the whole prompt inside that
// context; a truncated history is said rather than silently cut.
const (
	historyPromptChars  = 4000
	citationPromptChars = 1500
	itemsPromptMax      = 18
	itemQuoteChars      = 110
	consistencyDocChars = 4500
	suspectsMax         = 18
	suspectQuoteChars   = 110
)

// historyText is the retrieved history as the prompts take it, within the
// prompt budget. The documents are already reranked; the budget stops the
// prompt from growing with the knowledge base.
func historyText(docs []retrieval.DocHit) string {
	var b strings.Builder
	used := 0
	for i, doc := range docs {
		header := fmt.Sprintf("\n### 历史文档 %d：《%s》", i+1, doc.Title)
		if doc.Module != "" {
			header += fmt.Sprintf("（模块：%s）", doc.Module)
		}
		if doc.Date != "" {
			header += fmt.Sprintf(" 日期：%s", doc.Date)
		}
		header += "\n"
		n := len([]rune(header))
		if used+n > historyPromptChars {
			b.WriteString("\n…（其余历史片段已省略）\n")
			break
		}
		b.WriteString(header)
		used += n
		excerpt := truncate(doc.Excerpt, historyPromptChars-used)
		b.WriteString(excerpt)
		b.WriteString("\n")
		used += len([]rune(excerpt)) + 1
		if used >= historyPromptChars {
			b.WriteString("\n…（其余历史片段已省略）\n")
			break
		}
	}
	return b.String()
}

// evidenceRefs lists the chunk ids the model may cite with the text and the
// section each id names: the model answers with an id, and the pipeline has
// to be able to resolve it to a document, a section and a quote. Without the
// text beside the id the model guesses an id, and the evidence points at the
// wrong chunk or at nothing. The index has its own budget, cut at a chunk
// boundary.
func evidenceRefs(docs []retrieval.DocHit) string {
	var b strings.Builder
	used := 0
	for _, doc := range docs {
		for _, chunk := range doc.Chunks {
			line := fmt.Sprintf("[chunk_id=%d]《%s》%s\n", chunk.ChunkID, doc.Title, strings.Join(chunk.Path, " > "))
			text := truncate(strings.TrimSpace(chunk.Content), 160)
			n := len([]rune(line)) + len([]rune(text))
			if used+n > citationPromptChars {
				return b.String()
			}
			b.WriteString(line)
			if text != "" {
				b.WriteString("  " + text + "\n")
				used += 1
			}
			used += n
		}
	}
	return b.String()
}

// dedupeFindings drops conclusions the same run reached twice: the rule
// layer and the model both reporting one short requirement, or two parts of a
// long document quoting the same sentence for the same kind of finding. The
// evidence is part of the key, so two different historical conflicts quoted
// on the same sentence stay apart.
func dedupeFindings(findings []store.Finding) []store.Finding {
	out := findings[:0]
	at := make(map[string]int, len(findings))
	for _, f := range findings {
		key := f.Type + "|" + normForMatch(f.Span.Quote)
		// A one-liner is the same conclusion whether the model or the rule
		// layer wrote it; the other types use the subtype as part of their
		// meaning, so two gaps on one sentence stay apart.
		if f.Type != "one_liner" {
			key += "|" + f.Subtype
		}
		if strings.TrimSpace(f.Span.Quote) == "" {
			key += "|" + normForMatch(f.Title)
		}
		for _, e := range f.Evidence {
			key += "|" + normForMatch(e.Quote)
		}
		if i, ok := at[key]; ok {
			if richerFinding(f, out[i]) {
				out[i] = f
			}
			continue
		}
		at[key] = len(out)
		out = append(out, f)
	}
	return out
}

// richerFinding says which of two findings on the same sentence is the one to
// keep: the more confident, and the one that explains more.
func richerFinding(a, b store.Finding) bool {
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	return len([]rune(a.Detail)) > len([]rune(b.Detail))
}

// enforce drops what the contract does not allow: a finding whose quote is
// not in the new document, or a gap or signal with no history behind it.
// The quote check is what keeps a finding checkable: a reader must be able
// to find its sentence in the document the review is about.
func enforce(findings []store.Finding, chunks []store.Chunk) []store.Finding {
	out := findings[:0]
	for _, f := range findings {
		if strings.TrimSpace(f.Span.Quote) == "" {
			// Fall back to the first chunk, which is what the finding is
			// about when the model quoted nothing.
			if len(chunks) > 0 {
				f.Span.Quote = firstLine(chunks[0].Content)
				f.Span.Path = chunks[0].Path
			} else {
				continue
			}
		} else if !inDocument(chunks, f.Span.Quote) {
			// The quote is not in this document: the finding is about
			// something else, and a row a reader cannot check is not a
			// finding.
			continue
		}
		if f.Type == "logic_gap" || f.Type == "change_signal" {
			kept := f.Evidence[:0]
			for _, e := range f.Evidence {
				// A quote is checkable when it points at a chunk, or at the
				// document it came from: the ledger's own entries name their
				// document without holding its chunk ids.
				if e.Quote != "" && (e.ChunkID != 0 || e.DocTitle != "") {
					kept = append(kept, e)
				}
			}
			f.Evidence = kept
			if len(f.Evidence) == 0 {
				// Without history it is an ambiguity at best.
				f.Type, f.Subtype = "ambiguity", "undefined_reference"
				if f.Question == "" {
					f.Question = "这一改动与历史文档的关系需要确认：库中未找到可引用的历史规则。"
				}
			}
		}
		if f.Confidence < 0.5 {
			f.Severity = "S3"
		}
		if f.Severity == "" {
			f.Severity = "S2"
		}
		if f.Title == "" {
			continue
		}
		if len([]rune(f.Title)) > 40 {
			f.Title = string([]rune(f.Title)[:40])
		}
		out = append(out, f)
	}
	return out
}

// inDocument reports whether the new document itself holds the quote, and
// where. The comparison ignores spacing and punctuation, because a model
// re-types a quote rather than copying it.
func inDocument(chunks []store.Chunk, quote string) bool {
	_, ok := locateQuote(chunks, quote)
	return ok
}

// locateQuote finds the document's own sentence for a quote, and the chunk
// it sits in. An empty quote never matches, and a quote shorter than four
// characters matches anything, which is not a check.
//
// A long quote is allowed to differ at its ends: models quote the document
// and then trim or extend it, and a requirement is proved present by any
// stretch of quoteWindow characters that the document holds verbatim —
// which a paraphrase does not reproduce. Measured: one document's ten
// function points were all thrown away because every quote had been
// reworded at the edges.
func locateQuote(chunks []store.Chunk, quote string) (store.Chunk, bool) {
	want := normForMatch(quote)
	runes := []rune(want)
	if len(runes) < 4 {
		return store.Chunk{}, false
	}
	for _, chunk := range chunks {
		text := normForMatch(chunk.Content)
		if len(runes) <= quoteWindow {
			if strings.Contains(text, want) {
				return chunk, true
			}
			continue
		}
		for i := 0; i+quoteWindow <= len(runes); i += 3 {
			if strings.Contains(text, string(runes[i:i+quoteWindow])) {
				return chunk, true
			}
		}
	}
	return store.Chunk{}, false
}

// quoteWindow is how many characters of a quote the document must hold
// verbatim for the quote to count as its own sentence.
const quoteWindow = 14

// normForMatch keeps letters and digits only: a quote survives the spacing,
// the punctuation and the full-width forms that differ between the model's
// retyping and the document.
func normForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff, r >= 0x3400 && r <= 0x4dbf:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return truncate(strings.TrimSpace(line), 80)
		}
	}
	return ""
}

// fieldArray finds a field's raw value in a JSON object that may itself be
// cut off, by scanning for the key and the array that follows it.
func fieldArray(body []byte, key string) ([]byte, bool) {
	needle := []byte("\"" + key + "\"")
	i := bytes.Index(body, needle)
	if i < 0 {
		return nil, false
	}
	rest := body[i+len(needle):]
	j := bytes.IndexByte(rest, '[')
	if j < 0 {
		return nil, false
	}
	return rest[j:], true
}

// splitJSONArray reads the complete values of a JSON array that may stop in
// the middle of its last element: the decoder stops where the input does, so
// what arrived whole is returned and what did not is reported.
func splitJSONArray(raw []byte) ([]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, false
	}
	var out []json.RawMessage
	for decoder.More() {
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return out, false
		}
		out = append(out, value)
	}
	_, err := decoder.Token()
	return out, err == nil
}

// jsonBody trims a model's answer to its JSON: a fence or a sentence around
// it is common with a local model.
func jsonBody(answer string) ([]byte, error) {
	trimmed := strings.TrimSpace(answer)
	if i := strings.IndexAny(trimmed, "{["); i > 0 {
		trimmed = trimmed[i:]
	}
	if j := strings.LastIndexAny(trimmed, "}]"); j >= 0 && j < len(trimmed)-1 {
		trimmed = trimmed[:j+1]
	}
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("回答里没有 JSON：%s", snippet(answer))
	}
	return []byte(trimmed), nil
}

// snippet is a model answer as one line of a message: whitespace is
// collapsed and the cut is by runes, so a Chinese answer does not come back
// as a broken byte sequence.
func snippet(answer string) string {
	flat := strings.Join(strings.Fields(answer), " ")
	runes := []rune(flat)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return flat
}

// decodeLoose reads a model's list of objects under key. The contract asks
// for {"key":[{...}]}, but a local model also answers with a bare array, or
// with a string where an object was expected: one malformed element must
// not cost the whole answer. Elements that do not fit are skipped, and a
// string element goes to fromString when the caller can use one.
func decodeLoose[T any](answer, key string, fromString func(string) (T, bool)) ([]T, error) {
	out, _, err := decodeLoosePartial(answer, key, fromString)
	return out, err
}

// decodeLoosePartial is decodeLoose, and says whether the answer stopped in
// the middle: a generation that hit its token limit leaves a complete
// prefix and a half-written element. The complete elements are kept — a
// review that reached its limit is still a review — and the caller can say
// so.
func decodeLoosePartial[T any](answer, key string, fromString func(string) (T, bool)) ([]T, bool, error) {
	body, err := jsonBody(answer)
	if err != nil {
		return nil, false, err
	}
	var (
		raws      []json.RawMessage
		truncated bool
	)
	var envelope map[string]json.RawMessage
	switch {
	case body[0] == '{' && json.Unmarshal(body, &envelope) == nil:
		raw, ok := envelope[key]
		if !ok {
			// A model that answers {} says it found nothing; one that
			// answers with other keys answered a different question, and
			// that is worth a degradation.
			if len(envelope) == 0 {
				return []T{}, false, nil
			}
			return nil, false, fmt.Errorf("回答缺少 %s 字段：%s", key, snippet(answer))
		}
		values, complete := splitJSONArray(raw)
		raws, truncated = values, !complete
	case body[0] == '{':
		// The object itself is cut off: take the field's array if what
		// arrived so far holds it.
		raw, ok := fieldArray(body, key)
		if !ok {
			return nil, false, fmt.Errorf("回答不是 JSON：%s", snippet(answer))
		}
		values, complete := splitJSONArray(raw)
		raws, truncated = values, !complete
	default:
		values, complete := splitJSONArray(body)
		raws, truncated = values, !complete
	}
	if len(raws) == 0 && truncated {
		return nil, true, fmt.Errorf("回答被截断且没有完整条目：%s", snippet(answer))
	}
	out := make([]T, 0, len(raws))
	for _, raw := range raws {
		element := strings.TrimSpace(string(raw))
		switch {
		case element == "", element == "null":
		case element[0] == '"':
			if fromString == nil {
				continue
			}
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				continue
			}
			if value, ok := fromString(text); ok {
				out = append(out, value)
			}
		case element[0] == '{':
			var value T
			if err := json.Unmarshal(raw, &value); err != nil {
				continue
			}
			out = append(out, value)
		}
	}
	return out, truncated, nil
}

// chunkQuote finds the stored chunk a finding quotes, so that evidence
// carries the document, the section and the date.
func chunkQuotes(st *store.Store, ctx context.Context, refs map[int64]store.Evidence) map[int64]store.Evidence {
	out := make(map[int64]store.Evidence, len(refs))
	for id := range refs {
		if hit, ok, err := st.ChunkByID(ctx, id); err == nil && ok {
			e := refs[id]
			e.DocID, e.DocTitle, e.Path = hit.DocID, hit.DocTitle, hit.Path
			if !hit.DocDate.IsZero() {
				e.Date = hit.DocDate.Format("2006-01-02")
			}
			out[id] = e
		}
	}
	return out
}

// parseChunks reads a document for an analysis, from disk.
func ParseDocument(path string) (*ingest.Doc, []ingest.Chunk, error) {
	doc, err := ingest.Parse(path)
	if err != nil {
		return nil, nil, err
	}
	return doc, ingest.Chunking(doc.Blocks), nil
}
