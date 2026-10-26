package agent

import (
	"testing"

	"prior/store"
)

// TestUnresolvedChange covers the conflict rule: a changed rule with nothing
// said about what the old one already did is a question, and a document that
// does say is not asked.
func TestUnresolvedChange(t *testing.T) {
	chunks := func(text string) []store.Chunk {
		return []store.Chunk{{Index: 0, Content: text}}
	}
	cases := []struct {
		name   string
		before string
		after  string
		text   string
		open   bool
	}{
		{
			name:   "a changed rule with no transition",
			before: "退款在 7 个工作日内原路退回",
			after:  "退款在 3 个工作日内原路退回",
			text:   "本次需求把退款时限从 7 个工作日改为 3 个工作日，提升用户体验。",
			open:   true,
		},
		{
			name:   "the document says what happens to the old rule",
			before: "退款在 7 个工作日内原路退回",
			after:  "退款在 3 个工作日内原路退回",
			text:   "退款时限改为 3 个工作日；存量在途的退款申请仍按旧规则处理，不追溯。",
			open:   false,
		},
		{
			name:   "a backfill is named",
			before: "积分有效期为 12 个月",
			after:  "积分有效期为 24 个月",
			text:   "积分有效期调整为 24 个月，上线后对存量积分做一次刷数。",
			open:   false,
		},
		{
			name:   "the same rule in other words is not a change",
			before: "退款在 7 个工作日内原路退回",
			after:  "退款在7个工作日内原路退回。",
			text:   "退款说明。",
			open:   false,
		},
		{
			name:   "a new rule with no old one is not a conflict",
			before: "",
			after:  "新增退款入口",
			text:   "新增退款入口。",
			open:   false,
		},
	}
	for _, c := range cases {
		question, open := unresolvedChange(c.before, c.after, chunks(c.text))
		if open != c.open {
			t.Errorf("%s: open=%v, want %v (question %q)", c.name, open, c.open, question)
		}
		if open && question == "" {
			t.Errorf("%s: an unresolved change has to carry the question", c.name)
		}
		if open && !containsText(question, c.before) {
			t.Errorf("%s: the question does not cite the old rule: %q", c.name, question)
		}
	}
}

func containsText(s, part string) bool {
	return len(part) == 0 || len(s) >= len(part) && (indexOf(s, part) >= 0)
}

func indexOf(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
