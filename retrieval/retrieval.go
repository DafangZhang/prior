// Package retrieval finds the historical rules a new document has to be
// judged against: it writes the queries, recalls from both halves of the
// index, reranks what it found, and gathers it per document. It never
// imports ui.
package retrieval

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"prior/embed"
	"prior/store"
)

// Input is what a document says, which the queries are written from.
type Input struct {
	Title    string
	Module   string
	Headings []string
	Terms    []string
	Summary  string
}

// Options are the pipeline's numbers, as PRD-FINAL §9 fixes them.
type Options struct {
	// MaxQueries is the most queries one analysis writes.
	MaxQueries int
	// VectorK and KeywordK are how many chunks each half recalls.
	VectorK, KeywordK int
	// FuseK is the reciprocal rank fusion constant.
	FuseK int
	// TopChunks is how many fused candidates go on.
	TopChunks int
	// Rerank asks the model to score the candidates; TopRerank bounds it.
	Rerank      bool
	TopRerank   int
	RerankBatch int
	// TopDocs is how many documents the report names.
	TopDocs int
	// DocBudgetTokens is how much of one document enters the analysis.
	DocBudgetTokens int
}

// Defaults are the pipeline's numbers.
func Defaults() Options {
	return Options{
		MaxQueries: 30, VectorK: 50, KeywordK: 50, FuseK: 60, TopChunks: 200,
		// The rerank is a model call per batch: 24 candidates in batches of
		// 8 keep a review's rerank near a minute on one local model, where
		// 40 in batches of 20 took over three.
		Rerank: true, TopRerank: 24, RerankBatch: 8, TopDocs: 8, DocBudgetTokens: 4000,
	}
}

// DocHit is a document that holds rules the new document touches.
type DocHit struct {
	DocID   int64
	Title   string
	Module  string
	Date    string
	Score   float32
	Chunks  []store.Hit
	Excerpt string
}

// Result is what a search found.
type Result struct {
	Queries []string
	Chunks  []store.Hit
	Docs    []DocHit
	// Coverage is what an empty report explains: how many queries found
	// nothing, and how many candidates were looked at.
	Coverage Coverage
}

// Coverage is the search's own account of itself.
type Coverage struct {
	Queries    int
	Barren     int
	Candidates int
	Reranked   int
	Kept       int
}

// Search finds the history a document should be judged against.
func Search(ctx context.Context, st *store.Store, em *embed.Client, input Input, opts Options) (Result, error) {
	var res Result
	queries := queries(input, opts.MaxQueries)
	res.Queries = queries
	res.Coverage.Queries = len(queries)
	if len(queries) == 0 {
		return res, nil
	}

	vectors, err := em.Embed(ctx, queries)
	if err != nil {
		return res, fmt.Errorf("retrieval: 查询向量化失败：%w", err)
	}
	var lists [][]store.Hit
	for i, query := range queries {
		var vec, kw []store.Hit
		if i < len(vectors) {
			var err error
			if vec, err = st.SearchVector(ctx, vectors[i], opts.VectorK); err != nil {
				return res, err
			}
			if len(vec) > 0 {
				lists = append(lists, vec)
			}
		}
		var err error
		if kw, err = st.SearchKeyword(ctx, query, opts.KeywordK); err != nil {
			return res, err
		}
		if len(kw) > 0 {
			lists = append(lists, kw)
		}
		if len(vec) == 0 && len(kw) == 0 {
			res.Coverage.Barren++
		}
	}
	if len(lists) == 0 {
		return res, nil
	}
	fused := store.Fuse(opts.FuseK, lists...)
	res.Coverage.Candidates = len(fused)
	if len(fused) > opts.TopChunks {
		fused = fused[:opts.TopChunks]
	}

	if opts.Rerank && em.LLMModel() != "" && len(fused) > 0 {
		reranked, n, err := Rerank(ctx, em, input, fused, opts)
		if err != nil {
			// A rerank that fails leaves the fused order, which is the
			// pipeline's own degradation rule for a failed stage.
			res.Coverage.Reranked = -1
		} else {
			fused, res.Coverage.Reranked = reranked, n
		}
	} else {
		res.Coverage.Reranked = 0
	}
	res.Coverage.Kept = len(fused)
	res.Chunks = fused
	res.Docs = aggregate(fused, opts)
	return res, nil
}

// queries writes the searches a document calls for: what its sections are
// about, the terms it repeats, and what it says as a whole.
func queries(input Input, max int) []string {
	var out []string
	seen := map[string]bool{}
	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" || len([]rune(q)) < 4 || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, q)
	}
	// The module first: the same system's rules are the likeliest to
	// constrain a new document of it.
	if input.Module != "" {
		add(input.Module)
	}
	for _, h := range input.Headings {
		add(h)
	}
	// Terms travel in pairs, which keeps the query count down while the
	// pairs still stand for a topic.
	var pair strings.Builder
	for i, term := range input.Terms {
		if pair.Len() > 0 {
			pair.WriteString(" ")
		}
		pair.WriteString(term)
		if i%4 == 3 {
			add(pair.String())
			pair.Reset()
		}
	}
	add(pair.String())
	add(firstSentences(input.Summary, 3))
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// firstSentences takes the opening lines of a summary, which name what the
// document is about.
func firstSentences(text string, n int) string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len([]rune(line)) < 8 {
			continue
		}
		out = append(out, line)
		if len(out) == n {
			break
		}
	}
	return strings.Join(out, " ")
}

// aggregate gathers the hits per document: the document's score is its
// best chunk plus a tenth of the rest, and its content is what enters the
// analysis, within the budget and keeping the sentences that state rules.
func aggregate(hits []store.Hit, opts Options) []DocHit {
	byDoc := map[int64]*DocHit{}
	for _, hit := range hits {
		doc, ok := byDoc[hit.DocID]
		if !ok {
			doc = &DocHit{DocID: hit.DocID, Title: hit.DocTitle, Module: hit.Module}
			byDoc[hit.DocID] = doc
		}
		doc.Chunks = append(doc.Chunks, hit)
	}
	for _, doc := range byDoc {
		sort.SliceStable(doc.Chunks, func(i, j int) bool { return doc.Chunks[i].Score > doc.Chunks[j].Score })
		var sum float32
		for i, chunk := range doc.Chunks {
			if i == 0 {
				doc.Score = chunk.Score
				doc.Date = chunk.DocDate.Format("2006-01-02")
				continue
			}
			sum += chunk.Score
		}
		doc.Score += 0.1 * sum
		doc.Excerpt = budget(doc.Chunks, opts.DocBudgetTokens)
	}
	out := make([]DocHit, 0, len(byDoc))
	for _, doc := range byDoc {
		out = append(out, *doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if opts.TopDocs > 0 && len(out) > opts.TopDocs {
		out = out[:opts.TopDocs]
	}
	return out
}

// budget keeps a document's rules within a token budget, preferring the
// sentences that state a constraint: what a document must or must not do
// matters more to the analysis than what it explains.
func budget(chunks []store.Hit, tokens int) string {
	if tokens <= 0 {
		tokens = 4000
	}
	var b strings.Builder
	used := 0
	for _, chunk := range chunks {
		text := chunk.Content
		if used+tokensEstimate(text) > tokens {
			text = modalSentences(text)
			if used+tokensEstimate(text) > tokens {
				continue
			}
		}
		b.WriteString("【")
		if len(chunk.Path) > 0 {
			b.WriteString(strings.Join(chunk.Path, " > "))
		} else {
			b.WriteString("正文")
		}
		b.WriteString("】\n")
		b.WriteString(text)
		b.WriteString("\n\n")
		used += tokensEstimate(text)
	}
	return strings.TrimSpace(b.String())
}

// modalSentences keeps the sentences that state rules.
func modalSentences(text string) string {
	var out []string
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '。' || r == '\n' || r == '；' || r == ';'
	}) {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		for _, modal := range modalWords {
			if strings.Contains(sentence, modal) {
				out = append(out, sentence+"。")
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

var modalWords = []string{"必须", "不得", "应当", "禁止", "默认", "上限", "下限", "最多", "最少",
	"不超过", "至少", "仅在", "自动", "不再", "必须为", "统一", "不能"}

// tokensEstimate sizes a text for the budget.
func tokensEstimate(s string) int {
	cjk := 0
	for _, r := range s {
		if r >= 0x3000 {
			cjk++
		}
	}
	return cjk + (len(s)-cjk)/4
}
