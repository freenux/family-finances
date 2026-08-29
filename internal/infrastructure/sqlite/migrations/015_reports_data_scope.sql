-- +goose Up
-- +goose StatementBegin
-- 历史 AI 财报存档的口径标注。
--
-- 014 拆出统计口径（Scope）之后，ContextPack 的收入/支出/环比/结余率全部改成了日常口径
-- （剔除专项），页面文案也跟着改成「日常收入 / 日常支出 / 日常结余率」。但 reports 表里
-- 早先存下的那批报告，其 income_data / expense_data / kpi_data 是**全口径**算出来的——
-- 用新文案渲染旧存档，等于把全口径数字标成日常口径，存量数据被误标。
--
-- 解法是给每份存档打上它自己的口径标记，渲染时按存储值选文案（而不是重新生成历史报告：
-- 那要调 LLM、依赖 API key，还会改写历史）。默认值 'all' 正是存量行的真实口径，
-- 所以不需要任何数据回填。此后新生成的报告一律写 'daily'。
ALTER TABLE reports ADD COLUMN data_scope TEXT NOT NULL DEFAULT 'all';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE reports DROP COLUMN data_scope;
-- +goose StatementEnd
