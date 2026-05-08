-- +goose Up
-- +goose StatementBegin
INSERT INTO categories (id, parent_id, level, name, group_emoji, type, sort_order) VALUES
  ('income.salary',               NULL, 1, '工资收入',       '💼', 'income',  10),
  ('income.salary.husband',       'income.salary',     2, '男主税后工资', NULL, 'income',  11),
  ('income.salary.wife',          'income.salary',     2, '女主工资收入', NULL, 'income',  12),
  ('income.investment',           NULL, 1, '理财收入',       '💰', 'income',  20),
  ('income.investment.rent',      'income.investment', 2, '房屋租金',     NULL, 'income',  21),
  ('income.investment.interest',  'income.investment', 2, '存款/理财利息', NULL, 'income',  22),

  ('expense.fixed',               NULL, 1, '固定刚性支出',   '🔒', 'expense', 110),
  ('expense.fixed.education',     'expense.fixed',     2, '子女教育',     NULL, 'expense', 111),
  ('expense.fixed.parents',       'expense.fixed',     2, '父母赡养',     NULL, 'expense', 112),
  ('expense.fixed.insurance',     'expense.fixed',     2, '保险费用',     NULL, 'expense', 113),
  ('expense.fixed.housing',       'expense.fixed',     2, '居住成本',     NULL, 'expense', 114),

  ('expense.necessary',           NULL, 1, '变动必要支出',   '🛒', 'expense', 120),
  ('expense.necessary.food',      'expense.necessary', 2, '餐饮食材',     NULL, 'expense', 121),
  ('expense.necessary.home',      'expense.necessary', 2, '居家日常',     NULL, 'expense', 122),
  ('expense.necessary.transport', 'expense.necessary', 2, '交通出行',     NULL, 'expense', 123),
  ('expense.necessary.medical',   'expense.necessary', 2, '医疗健康',     NULL, 'expense', 124),

  ('expense.discretion',          NULL, 1, '自由裁量支出',   '🎯', 'expense', 130),
  ('expense.discretion.shopping', 'expense.discretion',2, '购物消费',     NULL, 'expense', 131),
  ('expense.discretion.leisure',  'expense.discretion',2, '娱乐休闲',     NULL, 'expense', 132),
  ('expense.discretion.social',   'expense.discretion',2, '社交人情',     NULL, 'expense', 133),
  ('expense.discretion.travel',   'expense.discretion',2, '旅游出行',     NULL, 'expense', 134),

  ('expense.family',              NULL, 1, '家庭专项支出',   '👨‍👩‍👧‍👧', 'expense', 140),
  ('expense.family.child_growth', 'expense.family',    2, '子女成长',     NULL, 'expense', 141),
  ('expense.family.parent_medical','expense.family',   2, '父母医疗',     NULL, 'expense', 142),
  ('expense.family.home_maintenance','expense.family', 2, '房屋维护',     NULL, 'expense', 143);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM categories;
-- +goose StatementEnd
