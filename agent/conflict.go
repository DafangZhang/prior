package agent

import (
	"regexp"
	"strings"

	"prior/store"
)

// The conflict rule: a document that changes a rule the ledger already holds
// has to say what happens to what the old rule governed. A document that says
// "退款改为 3 个工作日" without saying what happens to refunds already open
// under the old rule leaves the engineers a question they cannot answer, and
// that question is the finding.

// handledRE are the words a document uses when it does say:存量怎么处理,
// 新老如何兼容, 什么时候切换. Any one of them in the document is taken as an
// answer — a stricter rule would ask the model to judge, and a false finding
// about a document that did answer is worse than one that asks twice.
var handledRE = regexp.MustCompile(`存量|历史数据|旧数据|老数据|已(?:经)?生效|兼容|过渡期|灰度|` +
	`刷数|回刷|回溯|迁移|补数据|重新计算|重算|按新规则|按旧规则|新老|旧版|老版本|` +
	`不追溯|仅对(?:新增|新)|从.{0,6}(?:起|开始)生效|生效日期|切换时间|上线后|上线起|` +
	`保留原|维持原|维持不变|不受影响|保持不变|已提交的|已发起的|进行中的|在途`)

// unresolvedChange says whether a changed rule leaves the old one's
// consequences unaddressed: the document states the new rule and says nothing
// about what the old rule already did. It returns the question to ask when it
// does.
//
// The judgement is deliberately narrow. A change of wording alone is not a
// conflict — the ledger's identity rule already files that as clarified — and
// a document whose own words handle the transition is not asked about it.
func unresolvedChange(before, after string, chunks []store.Chunk) (string, bool) {
	if strings.TrimSpace(before) == "" || strings.TrimSpace(after) == "" {
		return "", false
	}
	if sameRule(before, after) {
		return "", false
	}
	text := documentText(chunks)
	if handledRE.MatchString(text) {
		return "", false
	}
	return "旧规则「" + truncate(before, 100) + "」的存量数据与已生效配置需要处置，但文档未说明", true
}

// sameRule says two rules say the same thing once their wording is levelled:
// a reworded rule is not a change a reviewer has to be asked about.
func sameRule(a, b string) bool {
	return normalizeRule(a) == normalizeRule(b)
}

// normalizeRule is a rule as its words compare: spaces and punctuation gone,
// so that "7 个工作日" and "7个工作日。" are the same rule.
func normalizeRule(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '\u3000' || r == '\n' || r == '\t':
			continue
		case strings.ContainsRune("，。；：、,.;:!?！？（）()「」\"'“”", r):
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// documentText is a document's body as the rule reads it.
func documentText(chunks []store.Chunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString(chunk.Content)
		b.WriteString("\n")
	}
	return b.String()
}
