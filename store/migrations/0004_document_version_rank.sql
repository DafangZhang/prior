-- 文档版本序（本轮修订）：账本要按「有日期 → 日期 → 版本 → 入库顺序」读，
-- 同一天的 V1.0.0 与 V1.1.0 才排得出先后。-1 表示版本读不出（空值或日期）。

ALTER TABLE documents ADD COLUMN IF NOT EXISTS version_rank integer NOT NULL DEFAULT -1;
