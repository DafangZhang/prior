package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Doc is a document as the knowledge base holds it.
type Doc struct {
	ID          int64
	Title       string
	SourceURI   string
	Format      string
	DocType     string
	ProductLine string
	Module      string
	Author      string
	Version     string
	Status      string
	Checksum    string
	Chars       int
	Chunks      int
	EmbedModel  string
	Date        time.Time
	DateSource  string
	DateConf    float64
	// VersionRank orders the document among the others of the same day: the
	// ledger reads a function point's trail from the older version to the
	// newer one. -1 is a document whose version cannot be read.
	VersionRank int
	Warnings    []string
	ImportedAt  time.Time
}

// Chunk is a stored piece of a document.
type Chunk struct {
	ID        int64
	DocID     int64
	Index     int
	Path      []string
	Content   string
	Tokens    int
	Embedding []float32
}

// Hit is a chunk found by a search, with the document it belongs to.
type Hit struct {
	ChunkID  int64
	DocID    int64
	DocTitle string
	DocDate  time.Time
	Module   string
	Path     []string
	Content  string
	Score    float32
	Source   string
}

// SaveDocument writes a document and its chunks, replacing what the same file
// stored before: ingesting the same folder twice changes nothing.
//
// The file is identified by its source path first, and by its checksum
// second. A file re-read after the parser changed still has its path and no
// longer has its checksum, and it is the same document — a copy of it read
// differently — not a second one.
func (s *Store) SaveDocument(ctx context.Context, doc Doc, chunks []Chunk, vectors [][]float32) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM documents
		WHERE source_uri = $1 OR checksum = $2
		ORDER BY (source_uri = $1) DESC, id DESC
		LIMIT 1`, doc.SourceURI, doc.Checksum).Scan(&id)
	switch {
	case err == nil:
		// The document is known: keep its row, replacing what it holds.
		if _, err := tx.Exec(ctx, `DELETE FROM chunks WHERE doc_id = $1`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE documents SET title=$2, source_uri=$3, format=$4, doc_type=$5, product_line=$6, checksum=$19,
				-- An empty module does not erase the system the document was
				-- filed under: a re-read that does not name one keeps it.
				module=coalesce(nullif($7,''), module), author=$8, version=$9, status=$10,
				char_count=$11, chunk_count=$12,
				embed_model=$13, doc_date=$14, date_source=$15, date_confidence=$16,
				version_rank=$18, parse_warnings=$17, updated_at=now()
			WHERE id=$1`,
			id, doc.Title, doc.SourceURI, doc.Format, doc.DocType, nullText(doc.ProductLine),
			nullText(doc.Module), nullText(doc.Author), nullText(doc.Version), doc.Status, doc.Chars, len(chunks),
			nullText(doc.EmbedModel), nullDate(doc.Date), nullText(doc.DateSource), doc.DateConf,
			JSON(doc.Warnings), doc.VersionRank, doc.Checksum); err != nil {
			return 0, err
		}
	default:
		if err := tx.QueryRow(ctx, `
			INSERT INTO documents (title, source_uri, format, doc_type, product_line, module, author,
				version, status, checksum, char_count, chunk_count, embed_model, doc_date, date_source,
				date_confidence, parse_warnings, version_rank)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			RETURNING id`,
			doc.Title, doc.SourceURI, doc.Format, doc.DocType, nullText(doc.ProductLine), nullText(doc.Module),
			nullText(doc.Author), nullText(doc.Version), doc.Status, doc.Checksum, doc.Chars, len(chunks),
			nullText(doc.EmbedModel), nullDate(doc.Date), nullText(doc.DateSource), doc.DateConf,
			JSON(doc.Warnings), doc.VersionRank).Scan(&id); err != nil {
			return 0, err
		}
	}

	for i, chunk := range chunks {
		var vec []float32
		if i < len(vectors) {
			vec = vectors[i]
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO chunks (doc_id, chunk_index, heading_path, content, token_count, tsv, embedding)
			VALUES ($1,$2,$3,$4,$5, to_tsvector('simple', $6), $7)`,
			id, chunk.Index, pathArray(chunk.Path), chunk.Content, chunk.Tokens,
			segmented(chunk.Content), vec); err != nil {
			return 0, err
		}
	}
	if s.vector && len(vectors) > 0 {
		if err := s.fillVectorColumn(ctx, tx, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// fillVectorColumn copies real[] vectors into the pgvector column, when the
// server has the extension.
func (s *Store) fillVectorColumn(ctx context.Context, tx pgx.Tx, docID int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE chunks SET embedding_vec = embedding::vector
		WHERE doc_id = $1 AND embedding IS NOT NULL`, docID)
	return err
}

// ListDocuments returns the documents, newest first, with the most recent
// first when their dates tie.
func (s *Store) ListDocuments(ctx context.Context, limit int) ([]Doc, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, source_uri, format, doc_type, coalesce(product_line,''), coalesce(module,''),
		       coalesce(author,''), coalesce(version,''), status, checksum, char_count, chunk_count,
		       coalesce(embed_model,''), doc_date, coalesce(date_source,''), date_confidence,
		       version_rank, parse_warnings, created_at
		FROM documents
		ORDER BY doc_date DESC NULLS LAST, version_rank DESC, id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (Doc, error) {
		var (
			d        Doc
			date     *time.Time
			warnings []byte
		)
		if err := rows.Scan(&d.ID, &d.Title, &d.SourceURI, &d.Format, &d.DocType, &d.ProductLine, &d.Module,
			&d.Author, &d.Version, &d.Status, &d.Checksum, &d.Chars, &d.Chunks, &d.EmbedModel, &date,
			&d.DateSource, &d.DateConf, &d.VersionRank, &warnings, &d.ImportedAt); err != nil {
			return d, err
		}
		d.Date = TimeOrZero(date)
		d.Warnings = decodeStrings(warnings)
		d.Title, d.Module, d.Author = cleanText(d.Title), cleanText(d.Module), cleanText(d.Author)
		return d, nil
	})
}

// DateSources counts where the documents' dates came from, which is how
// the archive says how much of it can be ordered by its own date.
func (s *Store) DateSources(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT coalesce(nullif(date_source,''),'missing'), count(*) FROM documents GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			source string
			n      int
		)
		if err := rows.Scan(&source, &n); err != nil {
			return nil, err
		}
		out[source] = n
	}
	return out, rows.Err()
}

// Modules lists the systems the library's documents have been assigned to,
// which is the vocabulary a new document's system is chosen from.
func (s *Store) Modules(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT module, count(*) FROM documents
		WHERE coalesce(module,'') <> ''
		GROUP BY module ORDER BY count(*) DESC, module`)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (string, error) {
		var (
			name string
			n    int
		)
		if err := rows.Scan(&name, &n); err != nil {
			return "", err
		}
		return name, nil
	})
}

// ModuleCount is one system and how many documents are filed under it.
type ModuleCount struct {
	Name  string
	Count int
}

// ModuleCounts lists the systems, largest group first.
func (s *Store) ModuleCounts(ctx context.Context) ([]ModuleCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT module, count(*) FROM documents
		WHERE coalesce(module,'') <> '' GROUP BY module ORDER BY count(*) DESC, module`)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (ModuleCount, error) {
		var m ModuleCount
		err := rows.Scan(&m.Name, &m.Count)
		return m, err
	})
}

// SetDocumentModule records the system a document belongs to.
func (s *Store) SetDocumentModule(ctx context.Context, docID int64, module string) error {
	_, err := s.pool.Exec(ctx, `UPDATE documents SET module = $2 WHERE id = $1`, docID, nullText(module))
	return err
}

// SetDocumentDate records the day a person gives a document. The document's
// records move with it: a record carries the date of the document it came
// from, and the ledger reads a function point's trail by document date, so one
// left on the old day would sort the trail by when it was read instead.
func (s *Store) SetDocumentDate(ctx context.Context, docID int64, date time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM documents WHERE id = $1)`, docID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("store: 文档 %d 不存在", docID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE documents SET doc_date=$2, date_source='manual', date_confidence=1, updated_at=now()
		WHERE id=$1`, docID, date); err != nil {
		return err
	}
	// Every record of a document belongs to that document's date, whether it
	// was written from the document's own date or, for a document that stated
	// none, from the day it was read: all of them follow.
	if _, err := tx.Exec(ctx, `UPDATE change_records SET recorded_at=$2 WHERE doc_id=$1`, docID, date); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateDocumentDate writes the date a re-read found. The records of the
// document keep the day they were recorded on, so that a re-read of the same
// document writes the same row twice.
func (s *Store) UpdateDocumentDate(ctx context.Context, docID int64, date time.Time, source string, conf float64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE documents SET doc_date=$2, date_source=$3, date_confidence=$4, updated_at=now()
		WHERE id=$1`, docID, nullDate(date), nullText(source), conf)
	return err
}

// SetDocumentVersionRank records the order of the version a document carries.
func (s *Store) SetDocumentVersionRank(ctx context.Context, docID int64, rank int) error {
	_, err := s.pool.Exec(ctx, `UPDATE documents SET version_rank=$2 WHERE id=$1`, docID, rank)
	return err
}

// Delete removes a document and what only it held: its chunks go with it,
// which the schema cascades. What pointed at it keeps its own row with the
// reference cleared — a run, a function point's first sighting, a change
// record's date — since what was reviewed from a document outlives it. It
// reports whether the document was there, so that a row deleted twice is
// told apart from one that was never deleted.
func (s *Store) Delete(ctx context.Context, docID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, docID)
	if err != nil {
		return false, fmt.Errorf("store: 删除文档失败：%w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DocumentChunks reads a document's chunks, in order, for the inspector.
func (s *Store) DocumentChunks(ctx context.Context, docID int64) ([]Chunk, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, doc_id, chunk_index, heading_path, content, coalesce(token_count,0)
		FROM chunks WHERE doc_id = $1 ORDER BY chunk_index`, docID)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (Chunk, error) {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.DocID, &c.Index, &c.Path, &c.Content, &c.Tokens); err != nil {
			return c, err
		}
		c.Content, c.Path = cleanText(c.Content), cleanPath(c.Path)
		return c, nil
	})
}

// DocumentByChecksum finds a document already stored, for the ingest's
// dedup.
func (s *Store) DocumentByChecksum(ctx context.Context, checksum string) (Doc, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM documents WHERE checksum = $1`, checksum).Scan(&id)
	if err != nil {
		return Doc{}, false, nil
	}
	return Doc{ID: id, Checksum: checksum}, true, nil
}

// CountChanges is what the ingest reports: how many records are new.
func (s *Store) Stats(ctx context.Context) (docs, chunks, findings, records int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM documents), (SELECT count(*) FROM chunks),
		       (SELECT count(*) FROM findings), (SELECT count(*) FROM change_records)`).
		Scan(&docs, &chunks, &findings, &records)
	return
}

// segmented is the text PostgreSQL's simple dictionary tokenizes: Chinese
// carries no spaces, so the pipeline writes its character bigrams as terms,
// which makes the keyword half of the hybrid search work without a
// dictionary the server would have to have.
func segmented(text string) string {
	var b strings.Builder
	var latin []rune
	flushLatin := func() {
		if len(latin) > 0 {
			b.WriteString(string(latin))
			b.WriteByte(' ')
			latin = latin[:0]
		}
	}
	runes := []rune(strings.ToLower(text))
	for i, r := range runes {
		switch {
		case isCJK(r):
			flushLatin()
			b.WriteRune(r)
			b.WriteByte(' ')
			if i+1 < len(runes) && isCJK(runes[i+1]) {
				b.WriteRune(r)
				b.WriteRune(runes[i+1])
				b.WriteByte(' ')
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			latin = append(latin, r)
		default:
			flushLatin()
		}
	}
	flushLatin()
	return b.String()
}

// isCJK says whether a rune is written without spaces between words.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

// pathArray is a heading path as PostgreSQL's array column takes it: a
// chunk under no heading stores an empty array, not a missing one.
func pathArray(path []string) []string {
	if path == nil {
		return []string{}
	}
	return path
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullDate(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func decodeStrings(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var out []string
	decodeJSON(data, &out)
	return out
}

func decodeJSON(data []byte, out any) {
	_ = json.Unmarshal(data, out)
}
