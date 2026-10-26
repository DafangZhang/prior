package agent

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"prior/embed"
	"prior/store"
)

// The pre-review is the first of the review's two readings: whether this
// document can be built at all, before asking what it changes. It works the
// way a technical pre-review does — the admission verdict, the gaps an
// engineer cannot start without, and the questions to send back — and it is
// built from three sources: the document's own sections (spec.go), the words
// that cannot be built from (the rule layer's vague classes), and the
// engineering dimensions a model reads the text for.

// preReviewPrompt asks for the gaps an engineer cannot start without. Every
// dimension is one the document has to answer somewhere; the answer is the
// document's own quote, so a gap it does state is not reported.
const preReviewPrompt = `你是研发负责人，正在预评审一份需求文档：判断它能不能开工，并指出研发无法开工的地方。
只写**文档确实缺的**东西：每条都要能指到文档里的一段原文，不要泛泛而谈，也不要重复文档已经写清的内容。

按下面的维度找，subtype 用括号里的英文：
- 角色与权限 (missing_actor)：谁触发这个操作？哪些角色受限？是否涉及跨租户或数据隔离？
- 前置条件与触发器 (missing_trigger)：什么用户交互、定时任务或系统事件触发？依赖哪些前置状态？
- 输入输出与数据契约 (missing_contract)：字段定义、校验规则、默认值、排序逻辑、分页方式是什么？
- 状态机 (missing_state)：实体有哪些状态？合法流转路径与逆向回滚条件是什么？
- 异常流 (exception_flow)：网络超时、第三方挂掉、空数据、重复提交（幂等）、并发修改冲突、部分失败的回滚或补偿？
- 数据生命周期与兼容 (data_compat)：存量数据如何迁移或刷数？新老版本是否兼容？软删还是硬删？缓存如何失效？
- 依赖与下游 (dependency)：影响哪些现有模块、计费/配额、消息队列或外部服务？
- 安全与合规 (security)：敏感数据脱敏、接口防刷与限流、操作审计、基于角色的权限控制？
- 可观测与验收 (observability)：埋点需求、监控告警指标、可测试可量化的验收标准？

每条给出：
- title：一句话说缺的是什么，不超过 20 字
- quote：逐字摘录文档中最相关的一段原文（10-80 字），必须能在文档里找到
- detail：为什么缺了它研发做不了，具体到实现或验收
- question：给产品经理的追问，一句话，可直接复制发给对方
- severity：S1（阻断开发）或 S2（可在开发中补齐）
- impact：影响哪些系统或角色，没有就留空

最多 10 条。文档写得完整就返回空数组，不要凑数。

只输出 JSON：{"findings":[{"subtype":"missing_actor","severity":"S1","title":"","detail":"","quote":"","heading_path":[],"question":"","impact":""}]}`

// preReviewFindings asks the model for the engineering gaps of a document, a
// part at a time: the parts that answered are kept, and one that failed is
// said rather than taking the whole reading with it.
func preReviewFindings(ctx context.Context, em *embed.Client, opts AnalyzeOptions) ([]store.Finding, error) {
	var (
		out      []store.Finding
		firstErr error
	)
	for _, part := range parts(opts.Chunks, opts.Profile.Headings) {
		notePart(opts, "P", part)
		findings, err := askPart(opts, "P", part, partPrompt, func(text string) ([]modelFinding, bool, error) {
			answer, err := em.Generate(ctx, preReviewPrompt, text, true, opts.temperature())
			if err != nil {
				return nil, false, err
			}
			return decodeFindings(answer)
		})
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("第 %d/%d 段：%w", part.Index, part.Total, err)
		}
		for _, f := range findings {
			f.Type = "pre_review"
			out = append(out, convert(f, nil))
		}
	}
	return out, firstErr
}

// preReviewOf runs the model's part of the pre-review and draws the verdict
// from everything found so far. The verdict is a rule over the findings rather
// than another model call: the same findings have to give the same verdict,
// and a verdict a reviewer cannot trace to a finding is one a review meeting
// cannot argue with.
func preReviewOf(ctx context.Context, em *embed.Client, opts AnalyzeOptions, items []Item, readErr error, soFar []store.Finding) preReview {
	findings, err := preReviewFindings(ctx, em, opts)
	all := append(slices.Clone(soFar), findings...)
	return preReview{Findings: findings, Review: verdictOf(items, readErr, err, all)}
}

// preReview is the whole judgement: what the reading found, and the verdict
// the findings add up to.
type preReview struct {
	Review   store.PreReview
	Findings []store.Finding
}

// verdictOf reads the findings into the verdict, the risks behind it, and the
// questions it asks.
func verdictOf(items []Item, readErr, gapErr error, findings []store.Finding) store.PreReview {
	var spec []store.Finding
	blockers, asks := 0, 0
	for _, f := range findings {
		if strings.HasPrefix(f.Subtype, "spec_") {
			spec = append(spec, f)
		}
		if f.Severity == "S0" || f.Severity == "S1" {
			blockers++
		}
		if strings.TrimSpace(f.Question) != "" {
			asks++
		}
	}
	unstructured := 0
	for _, item := range items {
		if strings.TrimSpace(item.Role) == "" && strings.TrimSpace(item.Action) == "" {
			unstructured++
		}
	}

	review := store.PreReview{}
	switch {
	case readErr != nil && len(items) == 0:
		// The document was not read: a verdict drawn from a failed reading
		// would be a guess, and the run says so instead.
		review.Verdict = "unknown"
		review.Summary = "需求条目没有读出来，无法定级：" + readErr.Error()
	case len(items) == 0:
		review.Verdict = "reject"
		review.Summary = "文档里读不出可开发的需求条目，只有描述性内容：按「打回重写」处理。"
	case len(spec) >= 3 && unstructured*2 >= len(items):
		review.Verdict = "reject"
		review.Summary = fmt.Sprintf("文档有 %d 条需求，其中 %d 条没写清谁在什么条件下做什么；另有 %d 个必备章节缺失：按「打回重写」处理。",
			len(items), unstructured, len(spec))
	case blockers > 0 || len(spec) > 0 || gapErr != nil:
		review.Verdict = "clarify"
		review.Summary = fmt.Sprintf("主流程可以读出来（%d 条需求），但有 %d 条阻断级问题、%d 个必备章节缺失：补齐后可进入排期。",
			len(items), blockers, len(spec))
	default:
		review.Verdict = "ready"
		review.Summary = fmt.Sprintf("文档有 %d 条需求，未见阻断级缺口：可以进入排期评审。", len(items))
	}
	if gapErr != nil {
		review.Summary += "（工程缺口有一段落没读成：" + gapErr.Error() + "）"
	}
	review.Risks = risksOf(findings, 3)
	review.Asked = questionsOf(findings, 20)
	return review
}

// risksOf names the biggest engineering risks: the worst findings first, as
// the two or three a review meeting should spend its time on.
func risksOf(findings []store.Finding, n int) []string {
	ranked := slices.Clone(findings)
	sort.SliceStable(ranked, func(i, j int) bool {
		if severityRank(ranked[i].Severity) != severityRank(ranked[j].Severity) {
			return severityRank(ranked[i].Severity) < severityRank(ranked[j].Severity)
		}
		return ranked[i].Confidence > ranked[j].Confidence
	})
	out := make([]string, 0, n)
	for _, f := range ranked {
		if len(out) == n {
			break
		}
		if strings.TrimSpace(f.Title) == "" {
			continue
		}
		risk := f.Severity + " · " + f.Title
		if detail := strings.TrimSpace(f.Detail); detail != "" {
			risk += "：" + truncate(detail, 90)
		}
		out = append(out, risk)
	}
	return out
}

// questionsOf is the question list a product manager receives: every question
// the review asks, worst first, without repeats.
func questionsOf(findings []store.Finding, n int) []string {
	ranked := slices.Clone(findings)
	sort.SliceStable(ranked, func(i, j int) bool {
		return severityRank(ranked[i].Severity) < severityRank(ranked[j].Severity)
	})
	seen := map[string]bool{}
	out := make([]string, 0, n)
	for _, f := range ranked {
		q := strings.TrimSpace(f.Question)
		if q == "" || seen[q] {
			continue
		}
		if len(out) == n {
			break
		}
		seen[q] = true
		out = append(out, q)
	}
	return out
}

// severityRank orders the severities the review uses, worst first.
func severityRank(severity string) int {
	switch severity {
	case "S0":
		return 0
	case "S1":
		return 1
	case "S2":
		return 2
	case "S3":
		return 3
	}
	return 4
}

// verdictLabel is a verdict as the stage log says it.
func verdictLabel(verdict string) string {
	switch verdict {
	case "ready":
		return "可进入排期"
	case "clarify":
		return "需补充细节后评审"
	case "reject":
		return "打回重写"
	}
	return "无法定级"
}
