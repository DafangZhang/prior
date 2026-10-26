-- 先例（Prior）核心表：与 PRD-FINAL §10 一致，含 round-7 的业务增量。
-- 向量列用 real[]，任何 PostgreSQL 都能建；pgvector 可用时由 0002 追加
-- 原生 vector 列与 HNSW 索引，检索自动改走索引。

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS documents (
  id            BIGSERIAL PRIMARY KEY,
  title         TEXT NOT NULL,
  source_uri    TEXT NOT NULL,
  format        TEXT NOT NULL,
  doc_type      TEXT NOT NULL DEFAULT 'unknown',
  product_line  TEXT,
  author        TEXT,
  version       TEXT,
  status        TEXT NOT NULL DEFAULT 'active',
  superseded_by BIGINT REFERENCES documents(id) ON DELETE SET NULL,
  checksum      TEXT NOT NULL,
  char_count    INTEGER NOT NULL DEFAULT 0,
  chunk_count   INTEGER NOT NULL DEFAULT 0,
  embed_model   TEXT,
  doc_date      DATE,
  date_source   TEXT,
  date_confidence REAL NOT NULL DEFAULT 0,
  module        TEXT,
  manifest_title  TEXT,
  manifest_link   TEXT,
  manifest_status TEXT,
  parse_warnings  JSONB NOT NULL DEFAULT '[]',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (checksum)
);
CREATE INDEX IF NOT EXISTS idx_documents_status  ON documents(status);
CREATE INDEX IF NOT EXISTS idx_documents_product ON documents(product_line);
CREATE INDEX IF NOT EXISTS idx_documents_module  ON documents(module);
CREATE INDEX IF NOT EXISTS idx_documents_date    ON documents(doc_date DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS idx_documents_title   ON documents USING gin (title gin_trgm_ops);

CREATE TABLE IF NOT EXISTS chunks (
  id          BIGSERIAL PRIMARY KEY,
  doc_id      BIGINT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  chunk_index INTEGER NOT NULL,
  heading_path TEXT[] NOT NULL DEFAULT '{}',
  content     TEXT NOT NULL,
  token_count INTEGER,
  tsv         TSVECTOR,
  embedding   real[],
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (doc_id, chunk_index)
);
CREATE INDEX IF NOT EXISTS idx_chunks_doc ON chunks(doc_id);
CREATE INDEX IF NOT EXISTS idx_chunks_tsv ON chunks USING gin (tsv);

CREATE TABLE IF NOT EXISTS analysis_runs (
  id            BIGSERIAL PRIMARY KEY,
  new_doc_id    BIGINT REFERENCES documents(id) ON DELETE SET NULL,
  title         TEXT,
  scope         JSONB NOT NULL DEFAULT '{}',
  models        JSONB NOT NULL DEFAULT '{}',
  status        TEXT NOT NULL,
  current_stage TEXT,
  stage_log     JSONB NOT NULL DEFAULT '[]',
  error         TEXT,
  started_at    TIMESTAMPTZ,
  finished_at   TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_runs_status ON analysis_runs(status);

CREATE TABLE IF NOT EXISTS findings (
  id            BIGSERIAL PRIMARY KEY,
  run_id        BIGINT NOT NULL REFERENCES analysis_runs(id) ON DELETE CASCADE,
  type          TEXT NOT NULL,
  subtype       TEXT,
  severity      TEXT NOT NULL,
  confidence    REAL NOT NULL,
  title         TEXT NOT NULL,
  detail        TEXT NOT NULL,
  new_doc_span  JSONB NOT NULL,
  evidence      JSONB NOT NULL DEFAULT '[]',
  question      TEXT,
  suggested_fix TEXT,
  impact        TEXT,
  status        TEXT NOT NULL DEFAULT 'open',
  reviewer_note TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_findings_run ON findings(run_id, type, severity);

-- 功能点：模块下的稳定身份，变更账本按它串时间线。
CREATE TABLE IF NOT EXISTS function_points (
  id           BIGSERIAL PRIMARY KEY,
  module       TEXT NOT NULL,
  function     TEXT NOT NULL,
  topic        TEXT NOT NULL DEFAULT '',
  label        TEXT NOT NULL,
  first_doc_id BIGINT REFERENCES documents(id) ON DELETE SET NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (module, function, topic)
);

CREATE TABLE IF NOT EXISTS change_records (
  id               BIGSERIAL PRIMARY KEY,
  function_point_id BIGINT NOT NULL REFERENCES function_points(id) ON DELETE CASCADE,
  doc_id           BIGINT REFERENCES documents(id) ON DELETE SET NULL,
  run_id           BIGINT REFERENCES analysis_runs(id) ON DELETE SET NULL,
  kind             TEXT NOT NULL,
  before_text      TEXT,
  after_text       TEXT,
  impact           TEXT,
  evidence         JSONB NOT NULL DEFAULT '[]',
  status           TEXT NOT NULL DEFAULT 'extracted',
  recorded_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_change_point ON change_records(function_point_id, recorded_at DESC);

CREATE TABLE IF NOT EXISTS ingest_jobs (
  id          BIGSERIAL PRIMARY KEY,
  root        TEXT NOT NULL,
  total       INTEGER NOT NULL DEFAULT 0,
  done        INTEGER NOT NULL DEFAULT 0,
  failed      JSONB NOT NULL DEFAULT '[]',
  parser_ver  TEXT,
  embed_model TEXT,
  status      TEXT NOT NULL,
  started_at  TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS eval_cases (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT NOT NULL,
  source_doc_id BIGINT REFERENCES documents(id) ON DELETE SET NULL,
  gold          JSONB NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS eval_results (
  id          BIGSERIAL PRIMARY KEY,
  case_id     BIGINT NOT NULL REFERENCES eval_cases(id) ON DELETE CASCADE,
  prompt_ver  TEXT NOT NULL,
  precision_p REAL,
  recall_p    REAL,
  detail      JSONB,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
