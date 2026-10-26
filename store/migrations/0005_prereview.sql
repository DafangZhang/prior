-- 预评审结论：一次评审对文档本身的定级（可排期 / 需补充 / 打回），
-- 以及最大的工程风险和给产品经理的追问清单。评审的每条细节仍写在
-- findings 里，这里只放结论，报告页开页时读它。
ALTER TABLE analysis_runs ADD COLUMN IF NOT EXISTS pre_review JSONB NOT NULL DEFAULT '{}';
