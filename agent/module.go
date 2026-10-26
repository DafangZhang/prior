package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"prior/embed"
)

// modulePrompt names the system a document belongs to. The vocabulary is
// closed on purpose: the library groups by this name and the function point
// ledger keys its modules on it, so one system written two ways splits a
// group in two. A document for a system the list does not have may still
// answer with a new name, which is how the list grows.
// moduleAnswer is the model's answer: the system name under "module".
type moduleAnswer struct {
	Module string `json:"module"`
}

const modulePrompt = `你在给一份产品需求文档归档。请判断它属于哪个**系统/平台**。

要求：
1. 优先从下面「已登记的系统」里选一个，输出时**原样照抄**该名字。
2. 确实都不合适时，给出一个新的系统名：不超过 12 个字，写清是什么系统（如「XX管理系统」「XX平台」），不要用需求名或功能名。
3. 只输出 JSON，不要解释。

{"module":""}`

// InferModule asks the model which system a document belongs to, with the
// library's systems as the vocabulary.
func InferModule(ctx context.Context, em *embed.Client, brief DocumentBrief, headings []string, sample string, vocabulary []string, llmModel string, temperature float64) (string, error) {
	if llmModel == "" {
		return "", fmt.Errorf("agent: 未设置语言模型")
	}
	var b strings.Builder
	b.WriteString("已登记的系统：\n")
	if len(vocabulary) == 0 {
		b.WriteString("- （暂无）\n")
	}
	for _, name := range vocabulary {
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	b.WriteString("\n文档标题：")
	b.WriteString(brief.Title)
	if brief.Module != "" {
		b.WriteString("\n文件名里的模块线索：")
		b.WriteString(brief.Module)
	}
	if len(headings) > 0 {
		b.WriteString("\n\n章节结构：\n")
		for i, h := range headings {
			if i == 25 {
				break
			}
			b.WriteString("- ")
			b.WriteString(h)
			b.WriteString("\n")
		}
	}
	if sample != "" {
		b.WriteString("\n正文（节选）：\n")
		b.WriteString(truncate(sample, 1500))
	}

	answer, err := em.Generate(ctx, modulePrompt, b.String(), true, temperatureOr(temperature))
	if err != nil {
		return "", err
	}
	name, err := moduleName(answer)
	if err != nil {
		return "", err
	}
	return name, nil
}

// moduleName reads the module out of the model's answer and keeps it to a
// name: an answer that came back as a long sentence is not a system name.
// The answer may be {"module":"..."} or the bare name.
func moduleName(answer string) (string, error) {
	name, ok := parseModuleAnswer(answer)
	if !ok {
		return "", fmt.Errorf("agent: 模块判定的回答读不出模块名：%s", snippet(answer))
	}
	name = strings.Trim(name, "「」《》\"'。，,.")
	if name == "" {
		return "", fmt.Errorf("agent: 模块判定返回空")
	}
	if len([]rune(name)) > 20 {
		return "", fmt.Errorf("agent: 模块判定给了句子而不是名字：%s", snippet(name))
	}
	return name, nil
}

// parseModuleAnswer reads the name out of the model's answer. The answer may
// be {"module":"..."}, a bare string, or — as one run produced — that object
// with its closing brace missing, which is still perfectly readable and
// should not cost the document its system.
func parseModuleAnswer(answer string) (string, bool) {
	if structured, err := decodeLoose[moduleAnswer](answer, "module", func(text string) (moduleAnswer, bool) {
		return moduleAnswer{Module: text}, strings.TrimSpace(text) != ""
	}); err == nil {
		for _, a := range structured {
			if strings.TrimSpace(a.Module) != "" {
				return strings.TrimSpace(a.Module), true
			}
		}
	}
	return moduleFromLooseText(answer)
}

// moduleFromLooseText reads the first quoted value after the key, which is
// what a cut-off {"module":"..."} holds. An answer that is only the name
// itself — "某系统" — is that same reading with no key in front of it.
func moduleFromLooseText(answer string) (string, bool) {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "\"") {
		var bare string
		if err := json.Unmarshal([]byte(trimmed), &bare); err == nil && strings.TrimSpace(bare) != "" {
			return strings.TrimSpace(bare), true
		}
	}
	i := strings.Index(answer, "\"module\"")
	if i < 0 {
		return "", false
	}
	rest := answer[i+len("\"module\""):]
	start := strings.IndexByte(rest, '"')
	if start < 0 {
		return "", false
	}
	end := strings.IndexByte(rest[start+1:], '"')
	if end < 0 {
		return "", false
	}
	value := strings.TrimSpace(rest[start+1 : start+1+end])
	if value == "" {
		return "", false
	}
	return value, true
}
