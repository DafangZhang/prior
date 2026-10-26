package agent

import "testing"

// TestParseModuleAnswer covers the shapes this call comes back in, including
// the cut-off object one run produced: a missing closing brace must not cost
// a document its system.
func TestParseModuleAnswer(t *testing.T) {
	cases := map[string]string{
		`{"module":"媒资管理系统"}`:                     "媒资管理系统",
		`{"module": "园区数字资产平台"}`:                  "园区数字资产平台",
		`{"module":"园区数字资产平台"`:                    "园区数字资产平台",
		"  ```json\n{\"module\":\"企业内容平台\"}\n```": "企业内容平台",
		`"数字资产管理系统"`:                              "数字资产管理系统",
		`{"module":" 煤资管理系统 "}`:                   "煤资管理系统",
		`{"module":"       "}`:                    "",
		`{"other":"x"}`:                           "",
	}
	for answer, want := range cases {
		got, ok := parseModuleAnswer(answer)
		if want == "" {
			if ok && got != "" {
				t.Errorf("parseModuleAnswer(%q) = %q, expected nothing", answer, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("parseModuleAnswer(%q) = %q, %v; want %q", answer, got, ok, want)
		}
	}
}
