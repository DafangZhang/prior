package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDecodeFunctionPointsTolerates(tt *testing.T) {
	cases := []struct {
		name, answer string
		want         int
	}{
		{"contract", `{"function_points":[{"function":"下单","rule":"必须校验库存"},{"function":"退款","rule":""}]}`, 2},
		{"bare array", `[{"function":"下单","rule":"必须校验库存"}]`, 1},
		{"strings where objects", `{"function_points":[{"function":"a","rule":"规则一"},"规则二",""]}`, 2},
		{"fence", "```json\n{\"function_points\":[{\"function\":\"a\",\"rule\":\"r\"}]}\n```", 0},
	}
	for _, c := range cases {
		got, _, err := decodeFunctionPoints(c.answer)
		if c.name == "fence" {
			if err == nil {
				tt.Logf("fence: parsed %d points (tolerated)", len(got))
			}
			continue
		}
		if err != nil {
			tt.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(got) != c.want {
			tt.Errorf("%s: got %d points, want %d (%v)", c.name, len(got), c.want, got)
		}
	}
}

// TestDecodeFindingsTolerates covers the same tolerance on the review's own
// output: one element of the wrong shape must not cost the findings list.
func TestDecodeFindingsTolerates(tt *testing.T) {
	answer := `{"findings":[{"title":"标题一","detail":"说明一"}, "这是模型漏了字段的一条", {"title":"","detail":"说明三"}, {"title":"标题四","detail":"说明四"}]}`
	findings, _, err := decodeFindings(answer)
	if err != nil {
		tt.Fatalf("a string element broke the whole list: %v", err)
	}
	if len(findings) != 2 {
		tt.Fatalf("expected the 2 complete findings, got %d (%v)", len(findings), findings)
	}
	if findings[0].Title != "标题一" || findings[1].Title != "标题四" {
		tt.Errorf("unexpected findings %v", findings)
	}
	items, err := decodeLoose[Item](`[{"need":"一句话"},{"need":"另一句"}]`, "items", nil)
	if err != nil || len(items) != 2 {
		tt.Errorf("bare array: %v %v", items, err)
	}
	if got, err := decodeLoose[Item](`{}`, "items", nil); err != nil || len(got) != 0 {
		tt.Errorf("an empty answer means nothing found, not a failure: %v %v", got, err)
	}
	if _, err := decodeLoose[Item](`{"other":[]}`, "items", nil); err == nil {
		tt.Error("an answer with other keys should be an error")
	}
}

// TestSnippetIsOneLineAndRuneSafe covers the message a user reads: the
// answer is flattened and cut by runes, not by bytes.
func TestSnippetIsOneLineAndRuneSafe(t *testing.T) {
	long := strings.Repeat("中文回答，", 40)
	got := snippet(long)
	if strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("the snippet kept line breaks: %q", got)
	}
	if len([]rune(got)) != 121 {
		t.Errorf("the snippet is %d runes, expected 120 plus the ellipsis", len([]rune(got)))
	}
	if !utf8.ValidString(got) {
		t.Errorf("the snippet cut a rune in half: %q", got)
	}
}

// TestDecodeLooseKeepsACompletePrefix covers an answer cut off by the
// token limit: the elements that arrived complete are kept, and the caller
// is told the answer was cut. Losing a whole review because the last
// element was half-written would be the wrong trade.
func TestDecodeLooseKeepsACompletePrefix(t *testing.T) {
	cut := `{"function_points":[{"function":"面包屑导航","rule":"面包屑导航需显示当前所在位置"},
		{"function":"专区封面","rule":"封面按 16:9 展示，右上角"` // stops mid-object
	points, truncated, err := decodeLoosePartial[struct {
		Function string `json:"function"`
		Rule     string `json:"rule"`
	}](cut, "function_points", nil)
	if err != nil {
		t.Fatalf("a cut answer should keep its complete prefix: %v", err)
	}
	if !truncated {
		t.Error("the answer was cut and should say so")
	}
	if len(points) != 1 || points[0].Function != "面包屑导航" {
		t.Errorf("expected the one complete point, got %v", points)
	}

	// The same tolerance for a bare array and for one cut before any
	// element finished.
	if got, cut, err := decodeLoosePartial[map[string]any](`[{"a":1},{"b":2}]`, "x", nil); err != nil || cut || len(got) != 2 {
		t.Errorf("a complete bare array: %v cut=%v err=%v", got, cut, err)
	}
	if _, cut, err := decodeLoosePartial[map[string]any](`{"items":[{"a":`, "items", nil); err == nil || !cut {
		t.Errorf("nothing complete should be an error, got cut=%v err=%v", cut, err)
	}
}

// TestSplitJSONArray covers the scanner on the shapes a model produces.
func TestSplitJSONArray(t *testing.T) {
	raw := []byte(`[{"a":"}"},{"b":"[x]"},[1,2],{"c":{"d":1}}]`)
	values, complete := splitJSONArray(raw)
	if !complete || len(values) != 4 {
		t.Fatalf("got %d values, complete=%v", len(values), complete)
	}
	if string(values[3]) != `{"c":{"d":1}}` {
		t.Errorf("nested object came back as %s", values[3])
	}
	if _, complete := splitJSONArray([]byte(`[{"a":1},{"b":2`)); complete {
		t.Error("an array without its closing bracket is not complete")
	}
}
