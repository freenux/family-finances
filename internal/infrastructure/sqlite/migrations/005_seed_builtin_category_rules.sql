-- +goose Up
-- +goose StatementBegin
CREATE TABLE category_rules_new (
    id            TEXT PRIMARY KEY,
    pattern       TEXT NOT NULL,
    pattern_type  TEXT NOT NULL,
    field         TEXT NOT NULL,
    category_id   TEXT,
    priority      INTEGER NOT NULL DEFAULT 10,
    source        TEXT NOT NULL DEFAULT 'builtin',
    is_active     INTEGER NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_id) REFERENCES categories(id)
);

INSERT INTO category_rules_new (id, pattern, pattern_type, field, category_id, priority, source, is_active, created_at)
SELECT id, pattern, pattern_type, field, category_id, priority, source, is_active, created_at
FROM category_rules;

DROP TABLE category_rules;
ALTER TABLE category_rules_new RENAME TO category_rules;
CREATE INDEX idx_rules_priority_active ON category_rules (priority, is_active);

INSERT OR IGNORE INTO category_rules (id, pattern, pattern_type, field, category_id, priority, source, is_active) VALUES
  ('builtin-skip-platform-transfer', '转账', 'exact', 'platform_category', NULL, 90, 'builtin', 1),
  ('builtin-skip-platform-red-packet', '转账红包', 'exact', 'platform_category', NULL, 90, 'builtin', 1),
  ('builtin-skip-platform-withdraw', '提现', 'exact', 'platform_category', NULL, 90, 'builtin', 1),
  ('builtin-skip-platform-deposit', '充值', 'exact', 'platform_category', NULL, 90, 'builtin', 1),
  ('builtin-skip-platform-credit-card', '信用卡还款', 'exact', 'platform_category', NULL, 90, 'builtin', 1),
  ('builtin-skip-platform-investment', '投资理财', 'exact', 'platform_category', NULL, 90, 'builtin', 1),

  ('builtin-alipay-food', '餐饮美食', 'exact', 'platform_category', 'expense.necessary.food', 100, 'builtin', 1),
  ('builtin-alipay-grocery', '食品酒水', 'exact', 'platform_category', 'expense.necessary.food', 100, 'builtin', 1),
  ('builtin-alipay-supermarket', '超市便利', 'exact', 'platform_category', 'expense.necessary.home', 100, 'builtin', 1),
  ('builtin-alipay-daily-goods', '日用百货', 'exact', 'platform_category', 'expense.necessary.home', 100, 'builtin', 1),
  ('builtin-alipay-digital', '数码电器', 'exact', 'platform_category', 'expense.discretion.shopping', 100, 'builtin', 1),
  ('builtin-alipay-clothing', '服饰装扮', 'exact', 'platform_category', 'expense.discretion.shopping', 100, 'builtin', 1),
  ('builtin-alipay-beauty', '美容美发', 'exact', 'platform_category', 'expense.discretion.shopping', 100, 'builtin', 1),
  ('builtin-alipay-sports', '运动户外', 'exact', 'platform_category', 'expense.discretion.leisure', 100, 'builtin', 1),
  ('builtin-alipay-leisure', '文化休闲', 'exact', 'platform_category', 'expense.discretion.leisure', 100, 'builtin', 1),
  ('builtin-alipay-transport', '交通出行', 'exact', 'platform_category', 'expense.necessary.transport', 100, 'builtin', 1),
  ('builtin-alipay-car', '爱车养车', 'exact', 'platform_category', 'expense.necessary.transport', 100, 'builtin', 1),
  ('builtin-alipay-hotel', '酒店住宿', 'exact', 'platform_category', 'expense.discretion.travel', 100, 'builtin', 1),
  ('builtin-alipay-travel', '旅游出行', 'exact', 'platform_category', 'expense.discretion.travel', 100, 'builtin', 1),
  ('builtin-alipay-medical', '医疗健康', 'exact', 'platform_category', 'expense.necessary.medical', 100, 'builtin', 1),
  ('builtin-alipay-education', '教育培训', 'exact', 'platform_category', 'expense.fixed.education', 100, 'builtin', 1),
  ('builtin-alipay-parenting', '母婴亲子', 'exact', 'platform_category', 'expense.family.child_growth', 100, 'builtin', 1),
  ('builtin-alipay-home-service', '生活服务', 'exact', 'platform_category', 'expense.necessary.home', 100, 'builtin', 1),
  ('builtin-alipay-home-improvement', '家居家装', 'exact', 'platform_category', 'expense.family.home_maintenance', 100, 'builtin', 1),
  ('builtin-alipay-public-service', '公共服务', 'exact', 'platform_category', 'expense.fixed.housing', 100, 'builtin', 1),
  ('builtin-alipay-insurance', '保险', 'exact', 'platform_category', 'expense.fixed.insurance', 100, 'builtin', 1),

  ('builtin-keyword-subway', '地铁', 'contains', 'any', 'expense.necessary.transport', 200, 'builtin', 1),
  ('builtin-keyword-bus', '公交', 'contains', 'any', 'expense.necessary.transport', 200, 'builtin', 1),
  ('builtin-keyword-taxi', '打车', 'contains', 'any', 'expense.necessary.transport', 200, 'builtin', 1),
  ('builtin-keyword-parking', '停车', 'contains', 'any', 'expense.necessary.transport', 200, 'builtin', 1),
  ('builtin-keyword-gas', '加油', 'contains', 'any', 'expense.necessary.transport', 200, 'builtin', 1),
  ('builtin-keyword-train', '火车', 'contains', 'any', 'expense.discretion.travel', 210, 'builtin', 1),
  ('builtin-keyword-flight', '机票', 'contains', 'any', 'expense.discretion.travel', 210, 'builtin', 1),
  ('builtin-keyword-hotel', '酒店', 'contains', 'any', 'expense.discretion.travel', 210, 'builtin', 1),
  ('builtin-keyword-coffee', '咖啡', 'contains', 'any', 'expense.necessary.food', 220, 'builtin', 1),
  ('builtin-keyword-tea', '奶茶', 'contains', 'any', 'expense.necessary.food', 220, 'builtin', 1),
  ('builtin-keyword-supermarket', '超市', 'contains', 'any', 'expense.necessary.food', 220, 'builtin', 1),
  ('builtin-keyword-hospital', '医院', 'contains', 'any', 'expense.necessary.medical', 230, 'builtin', 1),
  ('builtin-keyword-pharmacy', '药店', 'contains', 'any', 'expense.necessary.medical', 230, 'builtin', 1),
  ('builtin-keyword-school', '学校', 'contains', 'any', 'expense.fixed.education', 240, 'builtin', 1),
  ('builtin-keyword-training', '培训', 'contains', 'any', 'expense.fixed.education', 240, 'builtin', 1),
  ('builtin-keyword-property-fee', '物业', 'contains', 'any', 'expense.fixed.housing', 250, 'builtin', 1),
  ('builtin-keyword-utilities-water', '水费', 'contains', 'any', 'expense.fixed.housing', 250, 'builtin', 1),
  ('builtin-keyword-utilities-electricity', '电费', 'contains', 'any', 'expense.fixed.housing', 250, 'builtin', 1),
  ('builtin-keyword-broadband', '宽带', 'contains', 'any', 'expense.fixed.housing', 250, 'builtin', 1),
  ('builtin-keyword-movie', '电影', 'contains', 'any', 'expense.discretion.leisure', 270, 'builtin', 1),
  ('builtin-keyword-fitness', '健身', 'contains', 'any', 'expense.discretion.leisure', 270, 'builtin', 1),
  ('builtin-keyword-online-shopping-taobao', '淘宝', 'contains', 'any', 'expense.discretion.shopping', 280, 'builtin', 1),
  ('builtin-keyword-online-shopping-jd', '京东', 'contains', 'any', 'expense.discretion.shopping', 280, 'builtin', 1);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM category_rules WHERE source = 'builtin' AND id LIKE 'builtin-%';
-- +goose StatementEnd
