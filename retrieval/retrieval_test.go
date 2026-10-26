package retrieval

import (
	"strings"
	"testing"

	"prior/store"
)

// TestQueriesStayInBudget covers the query generator: a document yields a
// bounded set of searches, its module among them and no duplicates.
func TestQueriesStayInBudget(t *testing.T) {
	input := Input{
		Title: "购物车优化", Module: "SaaS 官网",
		Headings: []string{"一、背景", "二、加购流程", "三、结算页", "四、异常提示", "五、数据口径"},
		Terms:    []string{"购物车", "结算页", "优惠券", "叠加规则", "失效提示", "分页加载", "登录失效", "商品状态"},
		Summary:  "本次优化购物车的加购与结算路径。\n结算页需要展示优惠券。\n异常场景要有明确提示。",
	}
	queries := queries(input, 6)
	if len(queries) == 0 || len(queries) > 6 {
		t.Fatalf("expected 1..6 queries, got %d", len(queries))
	}
	seen := map[string]bool{}
	for _, q := range queries {
		if seen[q] {
			t.Errorf("duplicate query %q", q)
		}
		seen[q] = true
		if len([]rune(q)) < 4 {
			t.Errorf("query too short to search with: %q", q)
		}
	}
	if !seen["SaaS 官网"] {
		t.Errorf("the module is missing from %v", queries)
	}
}

// TestAggregateRanksDocuments covers the document score: the best chunk
// plus a tenth of the rest, and the budget keeping the rules.
func TestAggregateRanksDocuments(t *testing.T) {
	hits := []store.Hit{
		{ChunkID: 1, DocID: 10, DocTitle: "A", Score: 0.9, Content: "必须保留积分规则。\n这段是说明文字。"},
		{ChunkID: 2, DocID: 10, DocTitle: "A", Score: 0.5, Content: "不得随意清零。"},
		{ChunkID: 3, DocID: 20, DocTitle: "B", Score: 0.6, Content: "默认展示 10 条。"},
	}
	docs := aggregate(hits, Defaults())
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}
	if docs[0].Title != "A" {
		t.Errorf("A holds the best chunk and should lead, got %s", docs[0].Title)
	}
	if got, want := docs[0].Score, float32(0.9)+0.1*0.5; got < want-0.001 || got > want+0.001 {
		t.Errorf("document score is %v, expected %v", got, want)
	}
	if !strings.Contains(docs[0].Excerpt, "必须保留积分规则") {
		t.Errorf("the excerpt lost the rule: %q", docs[0].Excerpt)
	}
}

// TestBudgetKeepsModalSentences covers the context budget: when a document
// is too long, what enters the analysis is what states a rule.
func TestBudgetKeepsModalSentences(t *testing.T) {
	long := strings.Repeat("这是一段没有约束的描述文字。", 40)
	withRule := long + "\n本字段上限为 5000 元。\n必须记录操作日志。"
	hits := []store.Hit{{ChunkID: 1, DocID: 1, DocTitle: "A", Score: 0.9, Content: withRule}}
	out := budget(hits, 40)
	if !strings.Contains(out, "上限") && !strings.Contains(out, "必须") {
		t.Errorf("the budget dropped the rules: %q", out)
	}
	if len([]rune(out)) >= len([]rune(withRule)) {
		t.Errorf("the budget did not shorten anything: %d runes", len([]rune(out)))
	}
}
