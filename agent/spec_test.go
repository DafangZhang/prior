package agent

import (
	"strings"
	"testing"

	"prior/store"
)

func chunkOf(path string, content string) store.Chunk {
	return store.Chunk{Path: strings.Split(path, " > "), Content: content}
}

// TestSpecFindingsFindsWhatIsMissing covers the checklist: a document
// written the way this corpus writes them gets the findings its missing
// sections deserve, and a complete one gets none.
func TestSpecFindingsFindsWhatIsMissing(t *testing.T) {
	thin := []store.Chunk{chunkOf("1 需求", "支持用户在前端购买产品。\n点击购买后跳转支付页。")}
	findings := specFindings(thin, []string{"1 需求"})
	bySubtype := map[string]store.Finding{}
	for _, f := range findings {
		bySubtype[f.Subtype] = f
		if f.Type != "ambiguity" {
			t.Errorf("%s is not an ambiguity", f.Subtype)
		}
		if f.Span.Quote == "" || f.Question == "" {
			t.Errorf("%s has no quote or question", f.Subtype)
		}
	}
	for _, want := range []string{"spec_context_missing", "spec_scope_missing",
		"spec_exception_missing", "spec_data_caliber_missing", "spec_acceptance_missing"} {
		if _, ok := bySubtype[want]; !ok {
			t.Errorf("missed %s (found %v)", want, keys(bySubtype))
		}
	}
	if got := bySubtype["spec_acceptance_missing"].Severity; got != "S1" {
		t.Errorf("a missing acceptance标准 is S1, got %s", got)
	}
	if _, ok := bySubtype["spec_function_list_missing"]; ok {
		t.Error("a document that says 支持… does have a function description")
	}

	complete := []store.Chunk{
		chunkOf("背景与目标", "本次的背景是为了解决购买流程长的问题，目标是提升转化。"),
		chunkOf("本次范围", "本期范围包含购买页，不在本次范围的是退款。"),
		chunkOf("功能说明", "支持用户在前端购买产品，新增订单查询功能。字段包含订单号与金额，上限 5000 元，超过上限报错。"),
		chunkOf("异常与边界", "支付失败时回滚订单；超时后重试；无数据时展示空态。"),
		chunkOf("数据口径", "统计口径按订单创建时间，金额单位为元，默认值 0。"),
		chunkOf("验收标准", "验收条件：支付成功后订单状态为已支付。"),
	}
	if got := specFindings(complete, []string{"背景与目标", "本次范围", "功能说明", "异常与边界", "数据口径", "验收标准"}); len(got) != 0 {
		t.Errorf("a complete document should have no spec findings, got %d: %v", len(got), keysOf(got))
	}
	if got := specFindings(nil, nil); got != nil {
		t.Errorf("an empty document has nothing to check: %v", got)
	}
}

func keys(m map[string]store.Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOf(findings []store.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Subtype)
	}
	return out
}
