package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// vectorCache holds every chunk's vector, which the search uses when the
// server has no pgvector: at this corpus size a few thousand vectors of
// 1024 float32 fit in memory, and a query is then a scan in Go.
type vectorCache struct {
	mu      sync.RWMutex
	loaded  time.Time
	byID    map[int64][]float32
	title   map[int64]string
	path    map[int64][]string
	content map[int64]string
	docID   map[int64]int64
	dates   map[int64]time.Time
}

// SearchVector finds the chunks nearest a query vector, the pipeline's
// first half of recall.
func (s *Store) SearchVector(ctx context.Context, vector []float32, topK int) ([]Hit, error) {
	if topK <= 0 {
		topK = 50
	}
	if s.vector {
		return s.searchVectorIndex(ctx, vector, topK)
	}
	return s.searchVectorScan(ctx, vector, topK)
}

// searchVectorIndex asks pgvector for the nearest chunks.
func (s *Store) searchVectorIndex(ctx context.Context, vector []float32, topK int) ([]Hit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.doc_id, d.title, d.doc_date, coalesce(d.module,''), c.heading_path, c.content,
		       1 - (c.embedding_vec <=> $1::float4[]::vector) AS score
		FROM chunks c JOIN documents d ON d.id = c.doc_id
		WHERE c.embedding_vec IS NOT NULL
		ORDER BY c.embedding_vec <=> $1::float4[]::vector
		LIMIT $2`, vector, topK)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (Hit, error) {
		var (
			h    Hit
			date *time.Time
		)
		if err := rows.Scan(&h.ChunkID, &h.DocID, &h.DocTitle, &date, &h.Module, &h.Path, &h.Content, &h.Score); err != nil {
			return h, err
		}
		h.DocDate = TimeOrZero(date)
		h.Source = "vector"
		h.Content, h.Path, h.DocTitle = cleanText(h.Content), cleanPath(h.Path), cleanText(h.DocTitle)
		return h, nil
	})
}

// searchVectorScan computes cosine similarity over the chunks held in
// memory, which is what a server without pgvector can do.
func (s *Store) searchVectorScan(ctx context.Context, vector []float32, topK int) ([]Hit, error) {
	if err := s.loadVectors(ctx); err != nil {
		return nil, err
	}
	s.vectors.mu.RLock()
	defer s.vectors.mu.RUnlock()
	hits := make([]Hit, 0, len(s.vectors.byID))
	for id, vec := range s.vectors.byID {
		hits = append(hits, Hit{
			ChunkID:  id,
			DocID:    s.vectors.docID[id],
			DocTitle: s.vectors.title[s.vectors.docID[id]],
			Path:     s.vectors.path[id],
			Content:  s.vectors.content[id],
			Score:    Cosine(vector, vec),
			Source:   "vector",
		})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// loadVectors reads every chunk's vector once, and again when the store
// says the knowledge base changed.
func (s *Store) loadVectors(ctx context.Context) error {
	s.vectors.mu.RLock()
	fresh := s.vectors.byID != nil && time.Since(s.vectors.loaded) < 30*time.Second
	s.vectors.mu.RUnlock()
	if fresh {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.doc_id, d.title, d.doc_date, c.heading_path, c.content, c.embedding
		FROM chunks c JOIN documents d ON d.id = c.doc_id
		WHERE c.embedding IS NOT NULL`)
	if err != nil {
		return err
	}
	type row struct {
		id, docID int64
		title     string
		date      *time.Time
		path      []string
		content   string
		embedding []float32
	}
	items, err := scanRows(rows, func(rows pgx.Rows) (row, error) {
		var r row
		if err := rows.Scan(&r.id, &r.docID, &r.title, &r.date, &r.path, &r.content, &r.embedding); err != nil {
			return r, err
		}
		return r, nil
	})
	if err != nil {
		return err
	}
	s.vectors.mu.Lock()
	defer s.vectors.mu.Unlock()
	s.vectors.byID = make(map[int64][]float32, len(items))
	s.vectors.title = make(map[int64]string)
	s.vectors.path = make(map[int64][]string)
	s.vectors.content = make(map[int64]string)
	s.vectors.docID = make(map[int64]int64)
	s.vectors.dates = make(map[int64]time.Time)
	for _, r := range items {
		s.vectors.byID[r.id] = r.embedding
		s.vectors.docID[r.id] = r.docID
		s.vectors.title[r.docID] = cleanText(r.title)
		s.vectors.path[r.id] = cleanPath(r.path)
		s.vectors.content[r.id] = cleanText(r.content)
		s.vectors.dates[r.docID] = TimeOrZero(r.date)
	}
	s.vectors.loaded = time.Now()
	return nil
}

// SearchKeyword finds chunks whose stored terms match, the pipeline's
// other half of recall. The terms come from the query generator.
func (s *Store) SearchKeyword(ctx context.Context, query string, topK int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = 50
	}
	// The terms are ORed: a search for a sentence's words must find the
	// chunks that hold any of them, and ts_rank orders by how many it holds.
	tsq := tsqueryOf(query)
	if tsq == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.doc_id, d.title, d.doc_date, coalesce(d.module,''), c.heading_path, c.content,
		       ts_rank(c.tsv, to_tsquery('simple', $1)) AS score
		FROM chunks c JOIN documents d ON d.id = c.doc_id
		WHERE c.tsv @@ to_tsquery('simple', $1)
		ORDER BY score DESC
		LIMIT $2`, tsq, topK)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (Hit, error) {
		var (
			h    Hit
			date *time.Time
		)
		if err := rows.Scan(&h.ChunkID, &h.DocID, &h.DocTitle, &date, &h.Module, &h.Path, &h.Content, &h.Score); err != nil {
			return h, err
		}
		h.DocDate = TimeOrZero(date)
		h.Source = "keyword"
		h.Content, h.Path, h.DocTitle = cleanText(h.Content), cleanPath(h.Path), cleanText(h.DocTitle)
		return h, nil
	})
}

// tsqueryOf is the terms of a query as a tsquery: the words of the search,
// ORed so that a chunk holding any of them is recalled, with a stop on the
// longest ones so a long summary does not turn into thousands of terms.
func tsqueryOf(query string) string {
	terms := strings.Fields(segmented(query))
	seen := map[string]bool{}
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		if len([]rune(term)) < 2 && !(len(term) > 0 && term[0] < 0x80) {
			continue
		}
		if seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, "'"+strings.ReplaceAll(term, "'", "''")+"'")
		if len(out) == 64 {
			break
		}
	}
	return strings.Join(out, " | ")
}

// ChunkByID reads one chunk, for evidence a finding cites.
func (s *Store) ChunkByID(ctx context.Context, id int64) (Hit, bool, error) {
	var (
		h    Hit
		date *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT c.id, c.doc_id, d.title, d.doc_date, coalesce(d.module,''), c.heading_path, c.content
		FROM chunks c JOIN documents d ON d.id = c.doc_id WHERE c.id = $1`, id).
		Scan(&h.ChunkID, &h.DocID, &h.DocTitle, &date, &h.Module, &h.Path, &h.Content)
	if err != nil {
		return Hit{}, false, nil
	}
	h.DocDate = TimeOrZero(date)
	h.Content, h.Path, h.DocTitle = cleanText(h.Content), cleanPath(h.Path), cleanText(h.DocTitle)
	return h, true, nil
}

// Fuse merges recall lists by reciprocal rank fusion, the pipeline's rule
// with k = 60: a chunk found by both halves rises above one found by one.
func Fuse(k int, lists ...[]Hit) []Hit {
	if k <= 0 {
		k = 60
	}
	scores := map[int64]float32{}
	best := map[int64]Hit{}
	for _, list := range lists {
		for rank, hit := range list {
			scores[hit.ChunkID] += 1 / float32(k+rank+1)
			if prev, ok := best[hit.ChunkID]; !ok || hit.Score > prev.Score {
				best[hit.ChunkID] = hit
			}
		}
	}
	out := make([]Hit, 0, len(scores))
	for id, score := range scores {
		hit := best[id]
		hit.Score = score
		hit.Source = "fused"
		out = append(out, hit)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
