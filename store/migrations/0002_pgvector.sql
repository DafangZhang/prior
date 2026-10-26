-- pgvector 可用时的原生向量列与 HNSW 索引。0001 已用 real[] 落库，
-- 这里把它复制到原生列上，检索改走索引；扩展不在的环境跳过本迁移。

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS embedding_vec vector(1024);

CREATE INDEX IF NOT EXISTS idx_chunks_embedding ON chunks
  USING hnsw (embedding_vec vector_cosine_ops) WITH (m = 16, ef_construction = 64);
