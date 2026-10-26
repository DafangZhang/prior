-- 功能点身份解析的落库（round-8 §3.2.3）。
-- 匹配来源与相似度可追溯：exact/normalized 是自动命中，candidate 只给候选、
-- 由人确认合并，merge 记录人工合并的结果。

ALTER TABLE change_records ADD COLUMN IF NOT EXISTS matched_point_id bigint;
ALTER TABLE change_records ADD COLUMN IF NOT EXISTS match_score real NOT NULL DEFAULT 0;
ALTER TABLE change_records ADD COLUMN IF NOT EXISTS match_kind text NOT NULL DEFAULT 'new';

CREATE INDEX IF NOT EXISTS idx_change_records_match ON change_records (match_kind) WHERE match_kind = 'candidate';
