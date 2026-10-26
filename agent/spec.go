package agent

import (
	"regexp"
	"strings"

	"prior/store"
)

// A requirement document is judged twice: on what it says (the clarity and
// conflict checks) and on whether it is a requirement document at all. The
// second judgement is the one this file makes, from the sections the
// product's own PRD template requires — the checklist the
// product-manager-toolkit skill's template is reduced to here. Absence is
// the finding: a document with no acceptance criteria cannot be accepted,
// however well it is written.
type specSection struct {
	key      string
	subtype  string
	title    string
	severity string
	// headings that mark the section.
	headings *regexp.Regexp
	// words that show the section's subject is covered even without its own
	// heading, which most of these documents do.
	signals  *regexp.Regexp
	detail   string
	question string
}

var specSections = []specSection{
	{
		key: "context", subtype: "spec_context_missing", title: "缺少背景与目标",
		severity: "S2",
		headings: regexp.MustCompile(`背景|目的|目标|概述|简介|缘起|立项`),
		signals:  regexp.MustCompile(`背景|目的|目标|为了|痛点|现状`),
		detail:   "文档没有说明这段需求为什么做、要解决什么问题，评审时无法判断方向对不对。",
		question: "这段需求的背景与目标是什么？解决的是谁的什么问题？",
	},
	{
		key: "scope", subtype: "spec_scope_missing", title: "缺少范围说明（含范围外）",
		severity: "S2",
		headings: regexp.MustCompile(`范围|本期|本次|不在|后续|边界`),
		signals:  regexp.MustCompile(`本期范围|本次范围|不在本|不包含|后续版本|后续迭代|范围外`),
		detail:   "文档没有写清这次做到哪里、什么不做，评审无法判断是否漏项，开发也容易越界。",
		question: "本次范围包含哪些、明确不做什么？后续版本再做什么？",
	},
	{
		key: "functions", subtype: "spec_function_list_missing", title: "缺少功能说明清单",
		severity: "S2",
		headings: regexp.MustCompile(`功能|需求|页面|流程|模块|交互`),
		signals:  regexp.MustCompile(`支持|新增|增加|修改|调整|展示|上传|下载|删除|查询`),
		detail:   "文档没有可逐条对照的功能说明，无法确认覆盖了哪些功能点。",
		question: "这次要做哪些功能？能否按功能点分节逐条写清？",
	},
	{
		key: "exceptions", subtype: "spec_exception_missing", title: "缺少异常与边界处理",
		severity: "S2",
		headings: regexp.MustCompile(`异常|边界|失败|错误|容错|限制|约束`),
		signals:  regexp.MustCompile(`失败|异常|超时|回滚|空态|无数据|报错|降级|上限|不能超过|限制|兜底|重试`),
		detail:   "文档没有交代失败、超时、无数据、越界时怎么办，开发只能自己猜。",
		question: "失败、超时、无数据、超出上限时分别怎么处理？",
	},
	{
		key: "caliber", subtype: "spec_data_caliber_missing", title: "缺少数据口径",
		severity: "S2",
		headings: regexp.MustCompile(`字段|口径|数据|统计|取值|枚举|规则`),
		signals:  regexp.MustCompile(`字段|枚举|取值|上限|下限|默认值|单位|口径|统计规则|校验`),
		detail:   "文档没有说清涉及哪些字段、取值范围与统计口径，实现与验收会有歧义。",
		question: "涉及的字段、取值与统计口径分别是什么？",
	},
	{
		key: "acceptance", subtype: "spec_acceptance_missing", title: "缺少验收标准",
		severity: "S1",
		headings: regexp.MustCompile(`验收|测试|完成标准|判定`),
		signals:  regexp.MustCompile(`验收|测试用例|判定标准|完成标准|通过条件`),
		detail:   "文档没有可判定的验收标准，无法确认做到什么程度算完成——没有验收标准等于没有需求。",
		question: "每条需求的验收条件是什么？",
	},
}

// specFindings checks the document against the sections a requirement
// document has to have. It reads the document's own headings and body, and
// reports what is missing as findings about the document itself.
func specFindings(chunks []store.Chunk, headings []string) []store.Finding {
	if len(chunks) == 0 {
		return nil
	}
	body := new(strings.Builder)
	for _, chunk := range chunks {
		body.WriteString(chunk.Content)
		body.WriteString("\n")
	}
	text := body.String()
	quote, path := firstLine(chunks[0].Content), chunks[0].Path
	var out []store.Finding
	for _, section := range specSections {
		if specCovered(section, headings, text) {
			continue
		}
		out = append(out, store.Finding{
			Type: "ambiguity", Subtype: section.subtype, Severity: section.severity,
			Title: section.title, Detail: section.detail, Question: section.question,
			Confidence: 0.6,
			Span:       store.Span{Quote: quote, Path: path},
		})
	}
	return out
}

// specCovered reports whether a section is there: its own heading with
// something under it, or the subject covered in the body.
func specCovered(section specSection, headings []string, text string) bool {
	for _, heading := range headings {
		if section.headings.MatchString(heading) {
			return true
		}
	}
	return section.signals.MatchString(text)
}
