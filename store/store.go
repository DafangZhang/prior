// Package store keeps the knowledge base in PostgreSQL: documents, their
// chunks and vectors, analysis runs and findings, function points and the
// change ledger. It never imports ui.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is a connection pool over the knowledge base.
type Store struct {
	pool *pgxpool.Pool
	// vector says the server has pgvector, so search uses its index.
	vector bool
	// dims is the size of the vectors stored, read when they are written.
	dims int
	// vectors is the whole knowledge base's vectors, used when the server
	// has no pgvector to search with.
	vectors vectorCache
}

// Open connects to PostgreSQL at dsn and runs the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: 无法连接 PostgreSQL：%w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		// A database that is not there yet is created rather than reported:
		// the DSN alone is then enough to start with.
		if created, cerr := ensureDatabase(ctx, dsn, err); cerr != nil {
			return nil, fmt.Errorf("store: PostgreSQL 没有响应（%s）：%w", hostOf(dsn), cerr)
		} else if created {
			pool, err = pgxpool.New(ctx, dsn)
			if err != nil {
				return nil, fmt.Errorf("store: PostgreSQL 无法连接：%w", err)
			}
			if err := pool.Ping(ctx); err != nil {
				pool.Close()
				return nil, fmt.Errorf("store: 建库后仍连不上（%s）：%w", hostOf(dsn), err)
			}
		}
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if s.vectorAvailable(ctx) && !s.hasVectorColumn(ctx) {
		// The server ships pgvector but this database has no native column
		// yet: either the migration was skipped while the extension could
		// not be created, or the extension arrived later. Apply what adds
		// the column and its index.
		if err := s.apply(ctx, "migrations/0002_pgvector.sql"); err != nil {
			return nil, err
		}
	}
	s.vector = s.hasVector(ctx)
	if n, err := s.Dim(ctx); err == nil {
		s.dims = n
	}
	return s, nil
}

// ensureDatabase creates the DSN's database when the server says it does not
// exist, connecting to the maintenance database of the same server. It says
// whether it created one, and reports the original error when it cannot.
func ensureDatabase(ctx context.Context, dsn string, cause error) (bool, error) {
	var pgErr *pgconn.PgError
	if !errors.As(cause, &pgErr) || pgErr.Code != "3D000" {
		return false, cause
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return false, cause
	}
	name := config.Database
	if name == "" || name == "postgres" {
		return false, cause
	}
	config.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return false, cause
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return false, fmt.Errorf("建库 %s 失败：%w", name, err)
	}
	return true, nil
}

// Close releases the pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Pool exposes the pool for callers that need their own queries.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// HasVector says whether search runs on a pgvector index.
func (s *Store) HasVector() bool { return s.vector }

// Dim is the size of the vectors in the knowledge base, 0 while none is
// stored.
func (s *Store) Dim(ctx context.Context) (int, error) {
	var dim int
	// array_length of the first vector, which every row shares the size of.
	err := s.pool.QueryRow(ctx, `SELECT coalesce(max(array_length(embedding, 1)), 0) FROM chunks`).Scan(&dim)
	if err != nil {
		return 0, err
	}
	return dim, nil
}

// migrate applies the migrations the database has not seen, in name order,
// recording each in schema_migrations.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var seen bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&seen); err != nil {
			return err
		}
		if seen {
			continue
		}
		if name == "0002_pgvector.sql" && !s.vectorAvailable(ctx) {
			// The server cannot provide pgvector, so the file must not run:
			// keep the record so it is not retried, and say why in the
			// store's state: search falls back to real[].
			_, err := s.pool.Exec(ctx,
				`INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT DO NOTHING`, name)
			if err != nil {
				return err
			}
			continue
		}
		if err := s.apply(ctx, "migrations/"+name); err != nil {
			return fmt.Errorf("store: 迁移 %s 失败：%w", name, err)
		}
	}
	return nil
}

// apply runs one migration file inside a transaction.
func (s *Store) apply(ctx context.Context, path string) error {
	data, err := migrations.ReadFile(path)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(data)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT DO NOTHING`,
		strings.TrimPrefix(path, "migrations/")); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// vectorAvailable says the server ships the pgvector extension, so the
// migration that creates it can run.
func (s *Store) vectorAvailable(ctx context.Context) bool {
	var has bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`).Scan(&has)
	return err == nil && has
}

// hasVector says the extension is installed in this database.
func (s *Store) hasVector(ctx context.Context) bool {
	var has bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`).Scan(&has)
	return err == nil && has
}

func (s *Store) hasVectorColumn(ctx context.Context) bool {
	var has bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'chunks' AND column_name = 'embedding_vec')`).Scan(&has)
	return err == nil && has
}

// hostOf is a DSN's host, for a message without its password.
func hostOf(dsn string) string {
	if i := strings.LastIndex(dsn, "@"); i >= 0 {
		rest := dsn[i+1:]
		if j := strings.Index(rest, "/"); j > 0 {
			return rest[:j]
		}
		return rest
	}
	return "未指定"
}

// JSON is a convenience for the JSONB columns.
func JSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return data
}

// Cosine is the similarity of two vectors, 0 when either is empty, as the
// pipeline's thresholds are written for values between 0 and 1.
func Cosine(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (sqrt(na) * sqrt(nb)))
}

func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for range 8 {
		x = (x + v/x) / 2
	}
	return x
}

// TimeOrZero reads a nullable timestamp.
func TimeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// scanRows collects rows into a slice, closing them.
func scanRows[T any](rows pgx.Rows, scan func(pgx.Rows) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
