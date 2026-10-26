package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"prior/embed"
	"prior/ingest"
	"prior/retrieval"
	"prior/store"
)

// Item is one requirement a document states, as the analysis reads it.
type Item struct {
	ID      string   `json:"id"`
	Quote   string   `json:"quote"`
	Need    string   `json:"need"`
	Role    string   `json:"role"`
	Action  string   `json:"action"`
	Value   string   `json:"value"`
	Missing []string `json:"missing"`
	Path    []string `json:"heading_path"`
}

// modelFinding is what the model answers with: the pipeline fills in the
// evidence's own details from the ids it cites.
type modelFinding struct {
	Type        string   `json:"type"`
	Subtype     string   `json:"subtype"`
	Severity    string   `json:"severity"`
	Confidence  float64  `json:"confidence"`
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	Quote       string   `json:"quote"`
	HeadingPath []string `json:"heading_path"`
	Evidence    []struct {
		ChunkID int64  `json:"chunk_id"`
		Quote   string `json:"quote"`
	} `json:"evidence"`
	Question string `json:"question"`
	Fix      string `json:"suggested_fix"`
	Impact   string `json:"impact"`
	// The ledger's own fields, which only change signals carry.
	Module   string `json:"module"`
	Function string `json:"function"`
	Topic    string `json:"topic"`
	Kind     string `json:"kind"`
	Before   string `json:"before"`
	After    string `json:"after"`
}

// extractRequirementItems asks the model for the requirements a document
// states, a part at a time: the parts that answered are kept, and one that
// failed is said rather than taking the whole reading with it.
func extractRequirementItems(ctx context.Context, em *embed.Client, opts AnalyzeOptions) ([]Item, error) {
	var (
		out      []Item
		firstErr error
	)
	for _, part := range parts(opts.Chunks, opts.Profile.Headings) {
		notePart(opts, "S0", part)
		items, err := askPart(opts, "S0", part, partPrompt, func(text string) ([]Item, bool, error) {
			answer, err := em.Generate(ctx, itemsPrompt, text, true, opts.temperature())
			if err != nil {
				return nil, false, err
			}
			return decodeLoosePartial[Item](answer, "items", nil)
		})
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("第 %d/%d 段：%w", part.Index, part.Total, err)
		}
		out = append(out, items...)
	}
	return numberItems(dedupeItems(out)), firstErr
}

// partPrompt is what a prompt shows of one part of a document: the document's
// own sections, and the part's body.
func partPrompt(opts AnalyzeOptions, part Part) string {
	var b strings.Builder
	b.WriteString("文档：")
	b.WriteString(opts.Doc.Title)
	b.WriteString("\n模块：")
	b.WriteString(opts.Doc.Module)
	if part.Total > 1 {
		fmt.Fprintf(&b, "\n这是全文的第 %d/%d 段，只读这一段。", part.Index, part.Total)
	}
	b.WriteString("\n\n章节：\n")
	for _, h := range headings(part.Headings, 40) {
		fmt.Fprintf(&b, "- %s\n", h)
	}
	b.WriteString("\n正文：\n")
	b.WriteString(part.Text)
	return b.String()
}

// dedupeItems drops the same requirement read twice, as two parts can quote
// the same sentence where a section is repeated.
func dedupeItems(items []Item) []Item {
	seen := map[string]bool{}
	out := items[:0]
	for _, item := range items {
		key := strings.Join(strings.Fields(item.Quote), "")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

// numberItems gives the merged items their own ids: the model numbers each
// answer from one, and the one-liners cite them.
func numberItems(items []Item) []Item {
	for i := range items {
		items[i].ID = itoa(i + 1)
	}
	return items
}

// noteTruncated tells the run when a model answer was cut by the token
// limit: what arrived is used, and how much was lost is said rather than
// left for the reader to wonder about.
func noteTruncated(opts AnalyzeOptions, stage string, truncated bool, kept int) {
	if !truncated {
		return
	}
	opts.progress(Event{Stage: stage,
		Message: fmt.Sprintf("模型回答达到长度上限，已按完整条目保留 %d 条（提示：文档很长时可分段提交）", kept)})
}

// oneLiners turns requirements that are not written as requirements into
// findings, which is what the review is for.
func oneLiners(ctx context.Context, em *embed.Client, opts AnalyzeOptions, items []Item) ([]store.Finding, error) {
	if len(items) == 0 {
		return nil, nil
	}
	// The items go in batches: a document with a hundred requirements has
	// more of them than one answer can carry, and an answer cut in half used
	// to leave the whole stage with nothing.
	var out []store.Finding
	var firstErr error
	for start := 0; start < len(items); start += itemsPerCall {
		end := min(start+itemsPerCall, len(items))
		batch := items[start:end]
		if len(items) > itemsPerCall {
			opts.progress(Event{Stage: "S1", Message: fmt.Sprintf("条目 %d-%d/%d", start+1, end, len(items))})
		}
		findings, err := oneLinerBatch(ctx, em, opts, batch)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, findings...)
	}
	return out, firstErr
}

// itemsPerCall is how many requirement items one one-liner call reads: the
// answer asks for a finding an item, and an answer has a length.
const itemsPerCall = 25

// oneLinerBatch asks for one batch of items.
func oneLinerBatch(ctx context.Context, em *embed.Client, opts AnalyzeOptions, items []Item) ([]store.Finding, error) {
	var b strings.Builder
	b.WriteString("文档：")
	b.WriteString(opts.Doc.Title)
	b.WriteString("\n\n需求条目：\n")
	for _, item := range items {
		fmt.Fprintf(&b, "- id=%s 原文：%s\n", item.ID, truncate(item.Quote, 160))
		if item.Role != "" || item.Action != "" || item.Value != "" {
			fmt.Fprintf(&b, "  已读出的四元组：角色=%s 动作=%s 价值=%s\n", item.Role, item.Action, item.Value)
		}
		if len(item.Missing) > 0 {
			fmt.Fprintf(&b, "  缺失：%s\n", strings.Join(item.Missing, "、"))
		}
	}
	answer, err := em.Generate(ctx, oneLinerPrompt, b.String(), true, opts.temperature())
	if err != nil {
		return nil, err
	}
	findings, truncated, err := decodeFindings(answer)
	// An answer cut before its first complete entry is a cut answer too, and
	// the decoder reports it as an error: the batch is asked again over its
	// halves rather than left unread.
	cut := truncated || (err != nil && len(findings) == 0)
	if cut && len(items) > 1 {
		opts.progress(Event{Stage: "S1", Message: "这一批的回答达到长度上限，拆成两半重问"})
		mid := len(items) / 2
		var out []store.Finding
		for _, half := range [][]Item{items[:mid], items[mid:]} {
			got, err := oneLinerBatch(ctx, em, opts, half)
			out = append(out, got...)
			if err != nil {
				return out, err
			}
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	noteTruncated(opts, "S1", truncated, len(findings))
	out := make([]store.Finding, 0, len(findings))
	for _, f := range findings {
		f.Type = "one_liner"
		out = append(out, convert(f, nil))
	}
	return out, nil
}

// llmAmbiguities asks the model for what is not clear in the document, a part
// at a time.
func llmAmbiguities(ctx context.Context, em *embed.Client, opts AnalyzeOptions) ([]store.Finding, error) {
	var (
		out      []store.Finding
		firstErr error
	)
	for _, part := range parts(opts.Chunks, opts.Profile.Headings) {
		notePart(opts, "S2", part)
		findings, err := askPart(opts, "S2", part, partPrompt, func(text string) ([]modelFinding, bool, error) {
			answer, err := em.Generate(ctx, ambiguityPrompt, text, true, opts.temperature())
			if err != nil {
				return nil, false, err
			}
			return decodeFindings(answer)
		})
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("第 %d/%d 段：%w", part.Index, part.Total, err)
		}
		for _, f := range findings {
			f.Type = "ambiguity"
			out = append(out, convert(f, nil))
		}
	}
	return out, firstErr
}

// logicGaps compares the document with history: conflicts between documents
// and coverage the history has and this document lacks. What it compares is
// the document's own requirements — read part by part, so that a long document
// is represented whole — rather than the opening of its text. Inconsistencies
// inside the document are the other half of S3, in internalConflicts.
func logicGaps(ctx context.Context, st *store.Store, em *embed.Client, opts AnalyzeOptions, items []Item, history, citationIndex string) ([]store.Finding, error) {
	if strings.TrimSpace(history) == "" {
		return nil, fmt.Errorf("没有检索到历史文档")
	}
	var b strings.Builder
	b.WriteString("新需求文档《")
	b.WriteString(opts.Doc.Title)
	b.WriteString("》的要点：\n")
	b.WriteString(itemsText(items))
	b.WriteString("\n\n可引用的历史片段：\n")
	b.WriteString(history)
	b.WriteString("\n\n可引用的 chunk_id（只能从这里选，选与 quote 同段的 id）：\n")
	b.WriteString(citationIndex)
	answer, err := em.Generate(ctx, gapPrompt, b.String(), true, opts.temperature())
	if err != nil {
		return nil, err
	}
	findings, truncated, err := decodeFindings(answer)
	if err != nil {
		return nil, err
	}
	noteTruncated(opts, "S3", truncated, len(findings))

	// The model cites chunk ids; the report has to name the document, its
	// section and its date, or a reader cannot tell which history the gap is
	// against.
	refs := map[int64]store.Evidence{}
	for _, f := range findings {
		for _, e := range f.Evidence {
			refs[e.ChunkID] = store.Evidence{ChunkID: e.ChunkID, Quote: e.Quote}
		}
	}
	resolved := chunkQuotes(st, ctx, refs)
	out := make([]store.Finding, 0, len(findings))
	for _, f := range findings {
		f.Type = "logic_gap"
		finding := convert(f, refs)
		kept := finding.Evidence[:0]
		for _, ev := range finding.Evidence {
			e, ok := resolved[ev.ChunkID]
			if !ok {
				continue
			}
			e.Quote = ev.Quote
			kept = append(kept, e)
		}
		finding.Evidence = kept
		out = append(out, finding)
	}
	return out, nil
}

// internalConflicts finds the document contradicting itself: the same rule
// stated twice with different thresholds, a scope one section includes and
// another excludes, a version or date that disagrees with its own change log.
// Both sides are quotes from this document, and both have to be found in it,
// so a conflict a reader cannot point at is not reported.
func internalConflicts(ctx context.Context, em *embed.Client, opts AnalyzeOptions, items []Item) ([]store.Finding, error) {
	digest := documentDigest(opts.Chunks, consistencyDocChars)
	if strings.TrimSpace(digest) == "" {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("文档：")
	b.WriteString(opts.Doc.Title)
	b.WriteString("\n\n章节：\n")
	for _, h := range headings(opts.Profile.Headings, 40) {
		fmt.Fprintf(&b, "- %s\n", h)
	}
	if len(items) > 0 {
		b.WriteString("\n需求条目（按文档顺序）：\n")
		b.WriteString(itemsText(items))
	}
	b.WriteString("\n正文：\n")
	b.WriteString(digest)
	answer, err := em.Generate(ctx, consistencyPrompt, b.String(), true, opts.temperature())
	if err != nil {
		return nil, err
	}
	findings, truncated, err := decodeFindings(answer)
	if err != nil {
		return nil, err
	}
	noteTruncated(opts, "S3", truncated, len(findings))
	out := make([]store.Finding, 0, len(findings))
	for _, f := range findings {
		quote := strings.TrimSpace(f.Quote)
		other := ""
		if len(f.Evidence) > 0 {
			other = strings.TrimSpace(f.Evidence[0].Quote)
		}
		if quote == "" || other == "" {
			continue
		}
		first, okFirst := locateQuote(opts.Chunks, quote)
		second, okSecond := locateQuote(opts.Chunks, other)
		if !okFirst || !okSecond || normForMatch(quote) == normForMatch(other) {
			// A paraphrase or the same sentence twice is not a contradiction.
			continue
		}
		f.Type, f.Subtype = "logic_gap", "internal_conflict"
		if len(f.HeadingPath) == 0 {
			f.HeadingPath = first.Path
		}
		finding := convert(f, nil)
		finding.Span = store.Span{Quote: quote, Path: first.Path}
		finding.Evidence = []store.Evidence{{
			DocID: opts.Doc.ID, DocTitle: opts.Doc.Title, ChunkID: second.ID,
			Quote: other, Path: second.Path, Date: dateOfDoc(opts.Doc),
		}}
		if strings.TrimSpace(finding.Question) == "" {
			finding.Question = "这两处说法以哪一处为准？另一处需要怎么改？"
		}
		out = append(out, finding)
	}
	return out, nil
}

// documentDigest is the document's body as one prompt reads it, cut at a
// chunk boundary so that a quote is never split across two calls.
func documentDigest(chunks []store.Chunk, maxChars int) string {
	var b strings.Builder
	for _, chunk := range chunks {
		text := chunk.Content
		if b.Len() > 0 && b.Len()+len([]rune(text))+len(chunk.Path) > maxChars {
			break
		}
		if len(chunk.Path) > 0 {
			b.WriteString(strings.Join(chunk.Path, " > "))
			b.WriteString("\n")
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

// dateOfDoc is a document's date as evidence shows it, empty when it states
// none of its own.
func dateOfDoc(doc store.Doc) string {
	if doc.Date.IsZero() || doc.DateSource == "" || doc.DateSource == "missing" {
		return ""
	}
	return doc.Date.Format("2006-01-02")
}

// itemsText is a document's requirements as a prompt shows them: the quote and
// what it asks for, which is what the history is compared against. A long
// document has more items than the prompt budget carries, so the list is
// sampled evenly across the document instead of stopping at its opening: a
// gap in the last chapter is as much a gap as one in the first.
func itemsText(items []Item) string {
	chosen, omitted := items, 0
	if len(items) > itemsPromptMax {
		step := float64(len(items)-1) / float64(itemsPromptMax-1)
		chosen = make([]Item, 0, itemsPromptMax)
		for i := range itemsPromptMax {
			chosen = append(chosen, items[int(float64(i)*step)])
		}
		omitted = len(items) - len(chosen)
	}
	var b strings.Builder
	for _, item := range chosen {
		fmt.Fprintf(&b, "- [%s] %s", item.ID, truncate(item.Quote, itemQuoteChars))
		if need := strings.TrimSpace(item.Need); need != "" {
			b.WriteString("（要求：" + truncate(need, 80) + "）")
		}
		if len(item.Path) > 0 {
			b.WriteString("〔" + strings.Join(item.Path, " > ") + "〕")
		}
		b.WriteString("\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "…（按全文均匀抽取，另有 %d 条未列出）\n", omitted)
	}
	if b.Len() == 0 {
		b.WriteString("（文档没有读出需求条目）\n")
	}
	return b.String()
}

// changeSignals finds what the document changes against history, and
// writes each change into its function point's ledger.
func changeSignals(ctx context.Context, st *store.Store, em *embed.Client, opts AnalyzeOptions,
	history string, found retrieval.Result, runID int64) ([]store.Finding, int, int, error) {
	if strings.TrimSpace(history) == "" {
		return nil, 0, 0, fmt.Errorf("没有检索到历史文档")
	}
	// The rule layer picks the sentences that read as a change, which the
	// model then confirms: a document that says nothing about changing
	// anything has nothing to confirm.
	suspects := changeSuspects(opts.Chunks)
	if len(suspects) == 0 {
		return nil, 0, 0, nil
	}
	var b strings.Builder
	b.WriteString("新需求文档《")
	b.WriteString(opts.Doc.Title)
	b.WriteString("》中被粗筛出的变更语句：\n")
	for _, s := range suspects {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	b.WriteString("\n可引用的历史片段：\n")
	b.WriteString(history)
	b.WriteString("\n\n可引用的 chunk_id（只能从这里选，选与 quote 同段的 id）：\n")
	b.WriteString(evidenceRefs(found.Docs))
	answer, err := em.Generate(ctx, signalPrompt, b.String(), true, opts.temperature())
	if err != nil {
		return nil, 0, 0, err
	}
	findings, truncated, err := decodeFindings(answer)
	if err != nil {
		return nil, 0, 0, err
	}
	noteTruncated(opts, "S4", truncated, len(findings))

	refs := map[int64]store.Evidence{}
	for _, f := range findings {
		for _, e := range f.Evidence {
			refs[e.ChunkID] = store.Evidence{ChunkID: e.ChunkID, Quote: e.Quote}
		}
	}
	resolved := chunkQuotes(st, ctx, refs)

	// One record per change, under the function point it belongs to, with
	// the evidence resolved to the chunks the model cited.
	var out []store.Finding
	records, points := 0, map[int64]bool{}
	for _, f := range findings {
		f.Type = "change_signal"
		finding := convert(f, refs)
		kept := finding.Evidence[:0]
		for _, ev := range finding.Evidence {
			e, ok := resolved[ev.ChunkID]
			if !ok {
				continue
			}
			e.Quote = ev.Quote
			kept = append(kept, e)
		}
		finding.Evidence = kept
		out = append(out, finding)

		point := FunctionPoint{Module: f.Module, Function: f.Function, Topic: f.Topic}
		pointID, err := st.UpsertFunctionPoint(ctx, store.FunctionPoint{
			Module:   firstNonEmpty(f.Module, opts.Doc.Module, "未归类"),
			Function: firstNonEmpty(f.Function, "未命名功能"),
			Topic:    firstNonEmpty(f.Topic, strings.TrimSpace(f.Subtype)),
			Label:    labelOfPoint(point, opts.Doc.Module),
		}, opts.Doc.ID)
		if err != nil {
			return out, records, len(points), err
		}
		points[pointID] = true
		kind := firstNonEmpty(f.Kind, "modified")
		if _, err := st.SaveChangeRecord(ctx, store.ChangeRecord{
			PointID: pointID, DocID: opts.Doc.ID, RunID: runID, Kind: kind,
			Before: f.Before, After: firstNonEmpty(f.After, f.Detail), Impact: f.Impact,
			Evidence: finding.Evidence, Status: "extracted", Recorded: opts.Doc.Date,
		}); err != nil {
			return out, records, len(points), err
		}
		records++
	}
	return out, records, len(points), nil
}

// convert maps a model answer onto the stored finding, resolving the
// evidence it cites and applying the contract's defaults.
func convert(f modelFinding, refs map[int64]store.Evidence) store.Finding {
	out := store.Finding{
		Type: f.Type, Subtype: f.Subtype, Severity: f.Severity, Confidence: f.Confidence,
		Title: strings.TrimSpace(f.Title), Detail: strings.TrimSpace(f.Detail),
		Span:     store.Span{Quote: strings.TrimSpace(f.Quote), Path: f.HeadingPath},
		Question: strings.TrimSpace(f.Question), Fix: strings.TrimSpace(f.Fix),
		Impact: strings.TrimSpace(f.Impact), Status: "open",
	}
	if out.Confidence == 0 {
		out.Confidence = 0.6
	}
	if out.Severity == "" {
		out.Severity = "S2"
	}
	for _, e := range f.Evidence {
		if e.ChunkID == 0 || strings.TrimSpace(e.Quote) == "" {
			continue
		}
		ev := store.Evidence{ChunkID: e.ChunkID, Quote: strings.TrimSpace(e.Quote)}
		if refs != nil {
			if resolved, ok := refs[e.ChunkID]; ok {
				ev.DocID, ev.DocTitle, ev.Path, ev.Date = resolved.DocID, resolved.DocTitle, resolved.Path, resolved.Date
			}
		}
		out.Evidence = append(out.Evidence, ev)
	}
	return out
}

// decodeFindings reads a model's findings answer.
func decodeFindings(answer string) ([]modelFinding, bool, error) {
	findings, truncated, err := decodeLoosePartial[modelFinding](answer, "findings", nil)
	if err != nil {
		return nil, truncated, err
	}
	kept := findings[:0]
	for _, f := range findings {
		if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Detail) == "" {
			continue
		}
		kept = append(kept, f)
	}
	return kept, truncated, nil
}

// The rule layer: what a document's own words give away, without a model.
// The patterns are PRD-FINAL §8.4's six classes.
var (
	placeholderRE = regexp.MustCompile(`(?i)\b(TBD|TODO|FIXME|XXX)\b|待定|待补充|待确认|暂定|后续补充|xxx+`)
	referenceRE   = regexp.MustCompile(`详见\s*[《“"][^》”"]+[》”"]|参见\s*[《“"][^》”"]+[》”"]|见附件`)
	// The skill's three vague classes: a UI wording that names no state, a
	// performance wording that names no number, and an enumeration that leaves
	// its range open. Each is asked for the number or the list it left out.
	vagueRE      = regexp.MustCompile(`尽快|快速|友好|优化一下|大量|偶尔|实时地|适当|合理|若干|少量|较多|大概|差不多|尽量|尽可能|适度|一部分|某些`)
	vagueUIRE    = regexp.MustCompile(`友好(?:提示|提示语|交互)?|操作流畅|流畅|优化(?:展示|体验|交互|显示)|自适应|美观|简洁|清晰(?:地)?展示|合理(?:展示|排列|布局)|体验良好|用户友好|炫酷|高级感`)
	vaguePerfRE  = regexp.MustCompile(`实时|高并发|海量|大文件|秒级|毫秒级|快速(?:响应|返回|加载)|及时(?:推送|通知|同步)|高效|低延迟|高性能|不影响性能|尽量(?:快|少)`)
	openListRE   = regexp.MustCompile(`等等|及其他|等相关(?:字段|功能|模块|数据)|参考竞品|以此类推|包括但不限于`)
	branchRE     = regexp.MustCompile(`流程|流程图|步骤`)
	branchMissRE = regexp.MustCompile(`失败|异常|超时|回滚|空态|无数据|降级`)
	enumRE       = regexp.MustCompile(`[^。；\n]{2,20}等(功能|类型|字段|状态|模块|场景|渠道|方式)`)
	passiveRE    = regexp.MustCompile(`(会被|将被|需被|应被)[^。；\n]{0,12}(清理|同步|删除|回收|覆盖|更新|推送)`)
	changeRE     = regexp.MustCompile(`不再|废弃|下线|作废|之前的?逻辑[^。；\n]{0,8}(不要|不再)|全部改为|统一替换|统一改|由[^。；\n]{2,20}改为`)
	// A scope word with no object is not a requirement: "所有的都不要" does
	// not say which pages, fields or records, and no two readers implement
	// the same list. The noun has to follow the word for it to be a scope.
	openScopeRE = regexp.MustCompile(`(?:所有|全部|全都|一律|统统|任何)(?:的)?(?:都|也|均)?(?:不要|不用|去掉|去除|删除|隐藏|展示|显示|改成|改为|保留|清理|重置)`)
	// A time or version reference with no anchor is the same problem in time:
	// "之前的不要" does not name the document, the version or the rule.
	deicticRE = regexp.MustCompile(`(?:之前|以前|原来|原先|此前|上述|前面|刚才|现有|既有|原有|旧版|旧逻辑|老逻辑|历史)(?:的)?(?:逻辑|规则|版本|方案|内容|字段|功能|流程|需求|文档|页面|数据|设计|交互|文案|接口)?(?:都|也)?(?:不要|不用|去掉|去除|删除|废弃|改掉|改为|改成|替换|不作数)`)
)

// oneLinerVerbRE is what makes a short line a requirement rather than a
// section title: it asks the system to do something.
var oneLinerVerbRE = regexp.MustCompile(`(?i)支持|新增|增加|添加|展示|显示|跳转|打开|关闭|导出|导入|上传|下载|删除|移除|修改|更新|配置|同步|推送|校验|拦截|限制|默认|允许|禁止|提供|接入|兼容|排序|筛选|搜索|查询|发送|接收|记录|清理|回滚|重试|合并|拆分|置灰|隐藏|support|add|show|display|export|import|upload|download|delete|update|configure|sync|push|validate|allow|forbid|default|sort|filter`)

// oneLinerQualifierRE is what a short requirement has to say to be
// implementable: a trigger, a boundary or a number.
var oneLinerQualifierRE = regexp.MustCompile(`当|如果|若|一旦|在.{0,20}(?:时|后|前)|超过|不少于|不超过|至少|最多|上限|下限|以内|以上|以下|为空|失败|异常|超时|重试|次数|[0-9０-９]`)

// oneLinerRoleRE names who acts. A short requirement with a role is only
// missing its acceptance, not its actor.
var oneLinerRoleRE = regexp.MustCompile(`用户|运营|管理员|客服|商家|会员|买家|卖家|系统|前端|后端|接口|开发|测试|租户`)

// ruleLayer is the确定性 half of the clarity check: it runs on the
// document's own text and needs no model.
func ruleLayer(doc store.Doc, chunks []store.Chunk) []store.Finding {
	var out []store.Finding
	for _, chunk := range chunks {
		for _, sentence := range sentences(chunk.Content) {
			switch {
			case placeholderRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "placeholder", "S2",
					"存在占位符，需求未定稿", sentence,
					"这段描述里还有待定/占位内容（"+matchOf(placeholderRE, sentence)+"），开发无法据此实现。",
					"该占位内容计划在哪个版本补齐？"))
			case referenceRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "undefined_reference", "S2",
					"引用了未随文提供的材料", sentence,
					"文中提到「"+matchOf(referenceRE, sentence)+"」，但该材料不在知识库中，读者无法核对。",
					"这份材料能否随需求一起提供，或把关键规则摘进本文？"))
			case deicticRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "unresolved_reference", "S1",
					"「之前/原来」没有可核对的版本", sentence,
					"「"+matchOf(deicticRE, sentence)+"」没有写明是哪份文档、哪个版本或哪条规则；对于改动类要求，开发无法确认要删改的范围。",
					"这里说的「之前」是哪份文档、哪个版本或哪条规则？保留什么、删除什么？"))
			case openScopeRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "open_scope", "S2",
					"「所有/全部」没有说明范围", sentence,
					"「"+matchOf(openScopeRE, sentence)+"」没有给出对象或完整清单，开发与测试无法确认要改哪些页面、字段或数据。",
					"「所有」具体包含哪些页面、字段或数据？完整清单是什么？"))
			case enumRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "incomplete_enumeration", "S3",
					"用了「等」字列举，范围不闭合", sentence,
					"「"+matchOf(enumRE, sentence)+"」没有把范围写全，开发与测试无法确认边界。",
					"这里包含的完整清单是什么？"))
			case passiveRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "passive_actor", "S2",
					"被动句缺少执行方", sentence,
					"没写清由谁触发、在什么条件下发生。",
					"这条动作由哪个系统或角色在什么时机执行？"))
			case vaguePerfRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "unquantified_performance", "S2",
					"性能表述没有指标", sentence,
					"「"+matchOf(vaguePerfRE, sentence)+"」没有给出可验收的指标，开发无法判断做到什么程度算达标。",
					"这里要求的具体指标是什么（QPS 峰值、P99 延迟、单文件大小上限、同步还是异步）？"))
			case vagueUIRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "unquantified_ui", "S3",
					"交互表述无法验收", sentence,
					"「"+matchOf(vagueUIRE, sentence)+"」没有对应的设计稿节点、状态或截断规则，开发只能自己理解。",
					"这条交互对应设计稿的哪个节点？空态、加载态、超长文本分别怎么展示？"))
			case openListRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "open_enumeration", "S3",
					"范围没有封闭", sentence,
					"「"+matchOf(openListRE, sentence)+"」把边界留给了读者，本期到底做哪些无法确认。",
					"本期确切的清单是什么？明确不做的有哪些？"))
			case vagueRE.MatchString(sentence):
				out = append(out, ruleFinding(doc, chunk, "quantified_ambiguity", "S2",
					"用量词描述但没有量化", sentence,
					"「"+matchOf(vagueRE, sentence)+"」没有量化标准，验收时无法判定。",
					"这个要求的量化标准是什么（数量、时长、阈值）？"))
			}
		}
		// 分支缺失针对整段流程描述，而不是单句。
		if branchRE.MatchString(chunk.Content) && !branchMissRE.MatchString(chunk.Content) {
			out = append(out, store.Finding{
				Type: "ambiguity", Subtype: "missing_branch", Severity: "S2", Confidence: 0.55,
				Title:    "流程缺少异常与回滚分支",
				Detail:   "这一节描述了流程，但没有提到失败、超时、回滚或空态的处理。",
				Span:     store.Span{Quote: firstLine(chunk.Content), Path: chunk.Path},
				Question: "该流程在失败、超时、无数据时分别怎么处理？",
				Status:   "open",
			})
		}
	}
	return dedupe(out)
}

// oneLinerFindings is the rule layer of the one-liner check: a short line
// that asks the system to do something, with no trigger, boundary or number,
// is a requirement a developer has to invent the rest of. It runs beside the
// model's stage so that a requirement S0 never extracted is still reported.
func oneLinerFindings(chunks []store.Chunk) []store.Finding {
	var out []store.Finding
	for _, chunk := range chunks {
		headings := make(map[string]bool, len(chunk.Path))
		for _, p := range chunk.Path {
			headings[strings.TrimSpace(p)] = true
		}
		for _, sentence := range sentences(chunk.Content) {
			sentence = strings.TrimSpace(sentence)
			// A table row is not a sentence: its cells are read one by one,
			// and a cell such as "支持" says nothing on its own.
			if strings.Contains(sentence, "|") || strings.ContainsAny(sentence, "？?") {
				continue
			}
			if headings[sentence] || nonGoalRE.MatchString(sentence) {
				continue
			}
			// A scope or time reference already has its own, more specific
			// finding; reporting it twice makes the same sentence look like
			// two problems.
			if openScopeRE.MatchString(sentence) || deicticRE.MatchString(sentence) {
				continue
			}
			n := len([]rune(sentence))
			if n < 3 || n > 30 {
				continue
			}
			if !oneLinerVerbRE.MatchString(sentence) || oneLinerQualifierRE.MatchString(sentence) {
				continue
			}
			severity := "S2"
			if oneLinerRoleRE.MatchString(sentence) {
				// The actor is there; what is missing is the acceptance.
				severity = "S3"
			}
			out = append(out, store.Finding{
				Type: "one_liner", Subtype: "one_sentence", Severity: severity, Confidence: 0.6,
				Title:    "一句话需求：" + truncate(sentence, 24),
				Detail:   "这条需求只有一句话，没有写触发条件、边界或验收标准；开发只能自行理解，测试也无法判定是否完成。",
				Span:     store.Span{Quote: truncate(sentence, 120), Path: chunk.Path},
				Question: "这条需求由谁在什么条件下触发？做到什么程度算完成？",
				Status:   "open",
			})
		}
	}
	return dedupeFindings(out)
}

// nonGoalRE marks a sentence that says what the document does not cover: it
// is a scope statement, not a requirement left half-written.
var nonGoalRE = regexp.MustCompile(`不在本期|不涉及|不包含|非本期|暂不|不处理|不做|不修改|不改动|无需改动`)

// ruleFinding is one rule hit as a finding.
func ruleFinding(doc store.Doc, chunk store.Chunk, subtype, severity, title, sentence, detail, question string) store.Finding {
	return store.Finding{
		Type: "ambiguity", Subtype: subtype, Severity: severity, Confidence: 0.7,
		Title: title, Detail: detail,
		Span:     store.Span{Quote: truncate(sentence, 120), Path: chunk.Path},
		Evidence: nil, Question: question, Status: "open",
	}
}

// changeSuspects are the sentences that read as a change, which the model
// then confirms against history.
func changeSuspects(chunks []store.Chunk) []string {
	var out []string
	seen := map[string]bool{}
	for _, chunk := range chunks {
		for _, sentence := range sentences(chunk.Content) {
			if !changeRE.MatchString(sentence) || seen[sentence] {
				continue
			}
			seen[sentence] = true
			out = append(out, truncate(sentence, suspectQuoteChars))
			if len(out) == suspectsMax {
				return out
			}
		}
	}
	return out
}

// headings is the first n headings of a document, which is what a prompt
// shows of its structure.
func headings(list []string, n int) []string {
	if len(list) > n {
		return list[:n]
	}
	return list
}

// sentences splits a chunk into the sentences the rule layer reads. A
// requirement can be four characters long ("支持导出", "之前的不要"), so the
// floor is low; the patterns, not the length, decide what is a finding.
func sentences(text string) []string {
	var out []string
	for _, s := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '。' || r == '\n' || r == '；' || r == ';' || r == '！' || r == '!'
	}) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) >= 4 {
			out = append(out, s)
		}
	}
	return out
}

func matchOf(re *regexp.Regexp, s string) string {
	return strings.TrimSpace(re.FindString(s))
}

// dedupe drops the same finding found twice, as a pattern that matches
// every line of a chunk would otherwise produce.
func dedupe(findings []store.Finding) []store.Finding {
	seen := map[string]bool{}
	out := findings[:0]
	for _, f := range findings {
		key := f.Subtype + "|" + f.Span.Quote
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// ParseChunksForAnalysis reads a document and its chunks, as the window's
// analysis does before it calls Analyze.
func ParseChunksForAnalysis(path string) (*ingest.Doc, []ingest.Chunk, error) {
	doc, chunks, err := ParseDocument(path)
	if err != nil {
		return nil, nil, err
	}
	// A document that is analysed is also archived, so it is filed under a
	// system the same way an ingested one is — from its own words, and never
	// guessed. The model is not asked here: a review spends its model calls
	// on the review.
	if doc.Meta.Module == "" || !ingest.SystemKnown(doc.Meta.Module) {
		headings := make([]string, 0, len(doc.Meta.Title)+8)
		headings = append(headings, doc.Meta.Title)
		for _, block := range doc.Blocks {
			if block.Kind == ingest.KindHeading && strings.TrimSpace(block.Text) != "" {
				headings = append(headings, strings.TrimSpace(block.Text))
			}
		}
		if system, ok := ingest.SystemIn(headings...); ok {
			doc.Meta.Module = system
		}
	}
	return doc, chunks, nil
}
