package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"prior/embed"
	"prior/store"
)

// RerankPromptVersion names the rerank prompt, recorded with the run.
const RerankPromptVersion = "rerank-v1"

// RerankPrompt asks whether a historical passage states a rule that
// constrains what a new document asks for. The pipeline's rule is that
// relevance here means a rule-level relationship, not a shared topic: two
// documents about coupons are not related unless one rules on the other.
const RerankPrompt = `你在做需求文档的对照检索。下面给出一份**新需求文档**的要点，以及若干条**历史文档片段**。
请判断每条历史片段是否对这份新需求构成**规则级约束**——即它规定了新需求可能改变、违背或必须遵守的规则；仅仅是同一主题、同一个系统、同样的用词都不算。

对每条片段给出 0 到 1 的分：
- 0.9~1.0 直接规定同一功能点的规则，新需求很可能修改它
- 0.7~0.89 规定相关功能点的规则，存在冲突或影响可能
- 0.5~0.69 有间接约束（口径、字段、流程前置条件）
- 0.2~0.49 同主题但无规则关系
- 0~0.19 无关

只输出 JSON：{"scores":[{"id":123,"score":0.8,"why":"一句话理由"}]}`

// Rerank scores the candidates with the model and keeps those above the
// pipeline's threshold, in score order.
func Rerank(ctx context.Context, em *embed.Client, input Input, hits []store.Hit, opts Options) ([]store.Hit, int, error) {
	limit := opts.TopRerank
	if limit <= 0 || limit > len(hits) {
		limit = len(hits)
	}
	batch := opts.RerankBatch
	if batch <= 0 {
		batch = 20
	}
	candidates := hits[:limit]
	scored := make(map[int64]float32, len(candidates))
	brief := briefOf(input)

	for start := 0; start < len(candidates); start += batch {
		end := min(start+batch, len(candidates))
		part := candidates[start:end]
		var b strings.Builder
		b.WriteString(brief)
		b.WriteString("\n\n历史文档片段：\n")
		for _, hit := range part {
			fmt.Fprintf(&b, "\n[id=%d] 文档《%s》", hit.ChunkID, hit.DocTitle)
			if len(hit.Path) > 0 {
				fmt.Fprintf(&b, " 章节：%s", strings.Join(hit.Path, " > "))
			}
			b.WriteString("\n")
			b.WriteString(truncateRunes(hit.Content, 300))
			b.WriteString("\n")
		}
		answer, err := em.Generate(ctx, RerankPrompt, b.String(), true, 0)
		if err != nil {
			return nil, 0, err
		}
		var out struct {
			Scores []struct {
				ID    int64   `json:"id"`
				Score float32 `json:"score"`
				Why   string  `json:"why"`
			} `json:"scores"`
		}
		if err := json.Unmarshal([]byte(answer), &out); err != nil {
			return nil, 0, fmt.Errorf("retrieval: 重排回答不是 JSON：%w", err)
		}
		for _, item := range out.Scores {
			scored[item.ID] = item.Score
		}
	}

	kept := make([]store.Hit, 0, len(candidates))
	for _, hit := range candidates {
		score, ok := scored[hit.ChunkID]
		if !ok || score < 0.5 {
			continue
		}
		hit.Score = score
		hit.Source = "rerank"
		kept = append(kept, hit)
	}
	if len(kept) == 0 {
		// A rerank that kept nothing leaves the fused candidates: an
		// analysis with no history at all is worse than one with loose
		// history, and the report says what it looked at.
		return candidates, len(scored), nil
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Score > kept[j].Score })
	return kept, len(scored), nil
}

// briefOf is the new document's own summary, which the rerank judges
// against.
func briefOf(input Input) string {
	var b strings.Builder
	b.WriteString("新需求文档《")
	b.WriteString(input.Title)
	b.WriteString("》")
	if input.Module != "" {
		b.WriteString("（模块：")
		b.WriteString(input.Module)
		b.WriteString("）")
	}
	b.WriteString("\n章节：")
	b.WriteString(strings.Join(headings(input.Headings, 12), " / "))
	b.WriteString("\n要点：\n")
	b.WriteString(truncateRunes(input.Summary, 1800))
	return b.String()
}

func headings(list []string, n int) []string {
	if len(list) > n {
		return list[:n]
	}
	return list
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
