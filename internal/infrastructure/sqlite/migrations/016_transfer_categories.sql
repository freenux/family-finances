-- +goose Up
-- +goose StatementBegin
-- 「资金往来」——与 income. / expense. 并列的第三个顶层科目命名空间。
--
-- 由来：status 一个枚举原本扛了两件事——核对进度（pending_review/confirmed）
-- 和"算不算真实收支"（excluded 里混着规则判定的转账和用户手排的误记）。
-- 现在拆开：status 只回答"计不计入报表"，科目回答"为什么不计"。
--
-- 这三个科目上的流水一律以 status='excluded'（UI 标签「不计收支」）落地，
-- 判据集中在 domain.IsTransferCategory，写入点有三个：ImportBill 落地、
-- PATCH 改分类、LLM 回写。绝不能落 confirmed——SumByBuckets /
-- TopTransactions / ListForRecurring 只按 direction 过滤、不看科目。
--
-- 口径为什么自动正确：垫付出去 + 报销回来两头都不计，净影响为零；
-- 借出 + 收回同理；提现/还信用卡本来就只是钱换口袋。
-- 唯一不适用的是退款——原支出那笔是 confirmed 算进支出的，退款标不计收支
-- 没人冲减它。退款走手工配对（改原支出金额），所以这里不建 transfer.refund。
INSERT INTO categories (id, parent_id, level, name, group_emoji, type, sort_order) VALUES
  ('transfer',            NULL,       1, '资金往来 · 不计收支', '🔁', 'transfer', 200),
  ('transfer.internal',   'transfer', 2, '内部转账',            NULL, 'transfer', 201),
  ('transfer.loan',       'transfer', 2, '借出借入还款',        NULL, 'transfer', 202),
  ('transfer.reimburse',  'transfer', 2, '报销垫付',            NULL, 'transfer', 203);

-- 迁移 005 的 6 条内置跳过规则原本 category_id=NULL（"跳过导入"），
-- 命中的行落成 status=excluded 且没有科目——用户既看不见、也不知道为什么被排掉。
-- 改指到 transfer.internal 后，这些行在流水页上有名有姓，能被人工纠正
-- （一笔被误判成"转账"的学费，现在改得回来）。
UPDATE category_rules SET category_id = 'transfer.internal'
WHERE id IN (
  'builtin-skip-platform-transfer',
  'builtin-skip-platform-red-packet',
  'builtin-skip-platform-withdraw',
  'builtin-skip-platform-deposit',
  'builtin-skip-platform-credit-card',
  'builtin-skip-platform-investment'
);

-- priority 85 <（严格早于）上面那 6 条的 90：微信里个人借款/报销到账走的都是
-- "转账"这个交易类型，会先撞上 'builtin-skip-platform-transfer'（转账 exact）。
-- 只有把这几条排在它前面，备注里写了"借款/报销"的转账才能落到更准的科目上。
-- 故意不种通用的 '还款' 规则：'房贷还款'/'车贷还款' 会被一起卷走，
-- 那些是真实支出（或含利息），静默从报表里消失比没分类严重得多。
-- 信用卡还款已由 'builtin-skip-platform-credit-card' 精确命中，不需要它。
INSERT INTO category_rules (id, pattern, pattern_type, field, category_id, priority, source, is_active) VALUES
  ('builtin-transfer-reimburse', '报销', 'contains', 'any', 'transfer.reimburse', 85, 'builtin', 1),
  ('builtin-transfer-loan',      '借款', 'contains', 'any', 'transfer.loan',      85, 'builtin', 1),
  ('builtin-transfer-lend',      '借钱', 'contains', 'any', 'transfer.loan',      85, 'builtin', 1),
  ('builtin-transfer-payback',   '还钱', 'contains', 'any', 'transfer.loan',      85, 'builtin', 1);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 顺序不能换：transactions.category_id 和 category_rules.category_id 都有
-- FOREIGN KEY 指向 categories(id)，不先把引用解开，DELETE 科目会被 FK 挡住。
UPDATE transactions   SET category_id = NULL WHERE category_id LIKE 'transfer.%';
DELETE FROM category_rules WHERE id IN (
  'builtin-transfer-reimburse',
  'builtin-transfer-loan',
  'builtin-transfer-lend',
  'builtin-transfer-payback'
);
UPDATE category_rules SET category_id = NULL WHERE category_id LIKE 'transfer.%';
DELETE FROM categories WHERE id LIKE 'transfer.%' OR id = 'transfer';
-- +goose StatementEnd
