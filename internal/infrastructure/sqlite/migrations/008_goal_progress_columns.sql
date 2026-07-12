-- +goose Up
-- +goose StatementBegin
-- 财务目标补列（不动旧列）：
--   target_ym          目标年月 'YYYY-MM'，可空（空则回退 years_to_achieve 下界折算）
--   current_amount_fen 当前已积累金额（分），linked_codes 非空时被快照余额覆盖
--   linked_codes       JSON 数组，如 ["asset.deposit"]，关联资产科目自动取数
ALTER TABLE financial_goals ADD COLUMN target_ym TEXT;
ALTER TABLE financial_goals ADD COLUMN current_amount_fen INTEGER NOT NULL DEFAULT 0;
ALTER TABLE financial_goals ADD COLUMN linked_codes TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE financial_goals DROP COLUMN linked_codes;
ALTER TABLE financial_goals DROP COLUMN current_amount_fen;
ALTER TABLE financial_goals DROP COLUMN target_ym;
-- +goose StatementEnd
