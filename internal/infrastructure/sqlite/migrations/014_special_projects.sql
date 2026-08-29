-- +goose Up
-- +goose StatementBegin
-- 专项开支（装修/购车这类金额大、一次性的项目）。
-- 与科目正交的第三个维度：科目回答"钱花在什么上"，专项回答"算不算日常基线"，
-- 一个装修会横跨房屋维护/购物消费/居家日常多个科目，因此不能做成 category。
-- 专项仍然计入支出合计（不同于 status='excluded'），只是可以在统计口径里被剔除。
CREATE TABLE special_projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    started_on DATETIME,
    ended_on   DATETIME,              -- NULL = 进行中
    budget_fen INTEGER NOT NULL DEFAULT 0,
    note       TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 流水挂到专项上；NULL = 日常开支
ALTER TABLE transactions ADD COLUMN special_id TEXT REFERENCES special_projects(id);
CREATE INDEX idx_tx_special ON transactions (special_id);

-- 迁移 013 的覆盖索引必须重建：聚合 SQL 加上 special_id 过滤后，
-- 该列不在索引里会导致逐行回表，013 的优化（季度聚合 165ms→2.9ms）作废。
DROP INDEX IF EXISTS idx_tx_category_occurred;
CREATE INDEX idx_tx_category_occurred ON transactions (category_id, occurred_at, status, special_id, amount);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 先删掉引用 special_id 的索引，SQLite 才允许 DROP COLUMN
DROP INDEX IF EXISTS idx_tx_special;
DROP INDEX IF EXISTS idx_tx_category_occurred;
ALTER TABLE transactions DROP COLUMN special_id;
CREATE INDEX idx_tx_category_occurred ON transactions (category_id, occurred_at, status, amount);
DROP TABLE IF EXISTS special_projects;
-- +goose StatementEnd
