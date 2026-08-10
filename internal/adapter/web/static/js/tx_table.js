// Alpine 组件：流水列表的排序 / 筛选 / 汇总 / 就地编辑 + 周期切换。
// 首屏通过 SSR 嵌入的 #data-transactions / #data-categories bootstrap，
// 周期切换时 GET /api/transactions 刷新 rows。依赖 period_utils.js。
function txTable() {
  const txData = JSON.parse(document.getElementById('data-transactions').textContent || '[]');
  const catData = JSON.parse(document.getElementById('data-categories').textContent || '[]');
  const ruleEl = document.getElementById('data-rule');
  const ruleData = ruleEl ? JSON.parse(ruleEl.textContent || 'null') : null;
  const specialEl = document.getElementById('data-specials');
  const specialData = specialEl ? JSON.parse(specialEl.textContent || '[]') : [];

  const specialNameById = {};
  for (const s of specialData) specialNameById[s.id] = s.name;

  // 按一级分组组装二级科目，给下拉用
  const groups = [];
  const groupIndex = {};
  for (const c of catData) {
    if (c.level === 1) {
      const g = { groupId: c.id, groupName: c.name, items: [] };
      groupIndex[c.id] = g;
      groups.push(g);
    }
  }
  for (const c of catData) {
    if (c.level === 2 && groupIndex[c.parent_id]) {
      groupIndex[c.parent_id].items.push(c);
    }
  }

  const catNameById = {};
  for (const c of catData) catNameById[c.id] = c.name;

  return {
    rows: txData,
    groupedCategories: groups,
    catNameById,
    specials: specialData,
    specialNameById,

    // 周期状态
    granularity: 'month',
    periodKey:   '',
    account:     'family',
    loading:     false,
    errorMsg:    '',
    activeRule:  ruleData,
    applyingRule: false,

    // 批量归入专项
    batchSpecialID: '',
    batching: false,

    keyword: '',
    directionFilter: 'all',
    memberFilter: '',
    sourceFilter: ['alipay', 'wechat', 'manual', 'csv'],
    accountFilter: ['husband', 'wife'],
    statusFilter: ['pending_review', 'confirmed'],
    categoryFilter: '',
    specialFilter: '',

    sortKey: 'occurred_at',
    sortDir: 'desc',

    init() {
      const el = this.$el;
      this.granularity = granularityFromPeriodType(el.dataset.initialGranularity || 'monthly');
      this.periodKey = el.dataset.initialPeriod || defaultPeriodKey(this.granularity);
      this.account = el.dataset.initialAccount || 'family';
      if (this.activeRule) {
        this.keyword = this.activeRule.pattern || '';
        this.categoryFilter = '';
        this.statusFilter = ['pending_review', 'confirmed'];
      }
    },

    setGranularity(g) {
      if (this.granularity === g) return;
      this.granularity = g;
      this.periodKey = defaultPeriodKey(g);
      this.fetchTransactions();
    },

    shiftPeriod(delta) {
      const next = shiftPeriodKey(this.granularity, this.periodKey, delta);
      if (!next) return;
      this.periodKey = next;
      this.fetchTransactions();
    },

    async fetchTransactions() {
      this.loading = true;
      this.errorMsg = '';
      const q = new URLSearchParams({
        type:    periodTypeFromGranularity(this.granularity),
        period:  this.periodKey,
        account: this.account,
      });
      if (this.activeRule) q.set('rule_id', this.activeRule.id);
      window.history.replaceState(null, '', window.location.pathname + '?' + q.toString());
      try {
        const r = await fetch('/api/transactions?' + q.toString());
        if (!r.ok) throw new Error('HTTP ' + r.status + ' ' + (await r.text()));
        const data = await r.json();
        this.rows = data.transactions || [];
      } catch (e) {
        this.errorMsg = e.message;
      } finally {
        this.loading = false;
      }
    },

    get filtered() {
      const kw = this.keyword.trim().toLowerCase();
      const dir = this.directionFilter;
      const sources = new Set(this.sourceFilter);
      const accounts = new Set(this.accountFilter);
      const statuses = new Set(this.statusFilter);
      const catFilter = this.categoryFilter;
      const specialFilter = this.specialFilter;

      const memberFilter = this.memberFilter;
      let out = this.rows.filter((t) => {
        if (dir !== 'all' && t.direction !== dir) return false;
        if (memberFilter === '__none__') {
          if (t.member) return false;
        } else if (memberFilter && t.member !== memberFilter) {
          return false;
        }
        // csv:<模板名> 归并到 'csv' 复选项
        const srcKey = t.source && t.source.startsWith('csv:') ? 'csv' : t.source;
        if (!sources.has(srcKey)) return false;
        if (t.account && !accounts.has(t.account)) return false;
        if (!statuses.has(t.status)) return false;
        if (catFilter === '__none__') {
          if (t.category_id) return false;
        } else if (catFilter && t.category_id !== catFilter) {
          return false;
        }
        if (specialFilter === '__none__') {
          if (t.special_id) return false;
        } else if (specialFilter && t.special_id !== specialFilter) {
          return false;
        }
        if (kw) {
          const hay = (
            (t.counterparty || '') + ' ' +
            (t.description || '') + ' ' +
            (t.note || '') + ' ' +
            (t.raw_row || '')
          ).toLowerCase();
          if (!hay.includes(kw)) return false;
        }
        if (this.activeRule && !this.matchesRule(t, this.activeRule)) return false;
        return true;
      });

      const key = this.sortKey;
      const sign = this.sortDir === 'asc' ? 1 : -1;
      out.sort((a, b) => {
        let av = a[key], bv = b[key];
        if (key === 'category_id') {
          av = catNameById[av] || '';
          bv = catNameById[bv] || '';
        } else if (key === 'special_id') {
          av = specialNameById[av] || '';
          bv = specialNameById[bv] || '';
        }
        if (av == null) av = '';
        if (bv == null) bv = '';
        if (typeof av === 'number' && typeof bv === 'number') {
          return (av - bv) * sign;
        }
        return String(av).localeCompare(String(bv), 'zh-CN') * sign;
      });
      return out;
    },

    get memberOptions() {
      const set = new Set();
      for (const t of this.rows) if (t.member) set.add(t.member);
      return [...set].sort((a, b) => a.localeCompare(b, 'zh-CN'));
    },

    get totals() {
      let income = 0, expense = 0;
      for (const t of this.filtered) {
        if (t.direction === 'income') income += t.amount_fen;
        else expense += t.amount_fen;
      }
      return { income, expense, net: income - expense };
    },

    sortBy(key) {
      if (this.sortKey === key) {
        this.sortDir = this.sortDir === 'asc' ? 'desc' : 'asc';
      } else {
        this.sortKey = key;
        this.sortDir = 'desc';
      }
    },

    sortIcon(key) {
      if (this.sortKey !== key) return '↕';
      return this.sortDir === 'asc' ? '↑' : '↓';
    },

    sourceLabel(s) {
      if (s && s.startsWith('csv:')) return 'CSV·' + s.slice(4);
      return { alipay: '支付宝', wechat: '微信', manual: '手填' }[s] || s;
    },

    fmtYuan(fen) {
      const sign = fen < 0 ? '-' : '';
      fen = Math.abs(fen);
      const yuan = Math.floor(fen / 100);
      const cents = fen % 100;
      const s = yuan.toLocaleString('en-US');
      return `${sign}${s}.${String(cents).padStart(2, '0')}`;
    },

    resetFilters() {
      this.keyword = '';
      this.directionFilter = 'all';
      this.memberFilter = '';
      this.sourceFilter = ['alipay', 'wechat', 'manual', 'csv'];
      this.accountFilter = ['husband', 'wife'];
      this.statusFilter = ['pending_review', 'confirmed'];
      this.categoryFilter = '';
      this.specialFilter = '';
      this.activeRule = null;
      const q = new URLSearchParams(window.location.search);
      q.delete('rule_id');
      window.history.replaceState(null, '', window.location.pathname + (q.toString() ? '?' + q.toString() : ''));
    },

    ruleLabel() {
      if (!this.activeRule) return '';
      const field = {
        any: '任意字段',
        counterparty: '交易对方',
        description: '商品说明',
        platform_category: '平台分类',
      }[this.activeRule.field] || '任意字段';
      const kind = this.activeRule.pattern_type === 'exact' ? '等于' : '包含';
      return `${field} ${kind}「${this.activeRule.pattern}」`;
    },

    matchesRule(t, rule) {
      const pattern = String(rule.pattern || '').trim().toLowerCase();
      if (!pattern) return false;
      const values = this.ruleFieldValues(t, rule.field);
      return values.some((value) => {
        value = String(value || '').toLowerCase();
        if (rule.pattern_type === 'exact') return value === pattern;
        return value.includes(pattern);
      });
    },

    ruleFieldValues(t, field) {
      if (field === 'counterparty') return [t.counterparty];
      if (field === 'description') return [t.description];
      if (field === 'platform_category') return [t.platform_category];
      return [t.counterparty, t.description, t.platform_category, t.note, t.raw_row];
    },

    get ruleApplyTargets() {
      if (!this.activeRule || !this.activeRule.category_id) return [];
      const categoryID = this.activeRule.category_id;
      return this.filtered.filter((t) => !(t.category_id === categoryID && t.status === 'confirmed'));
    },

    async applyRule() {
      const targets = [...this.ruleApplyTargets];
      if (!this.activeRule || targets.length === 0 || this.applyingRule) return;
      this.applyingRule = true;
      const categoryID = this.activeRule.category_id;
      let okCount = 0;
      try {
        for (const t of targets) {
          const prevCategory = t.category_id;
          const prevStatus = t.status;
          t.category_id = categoryID;
          t.status = 'confirmed';
          const ok = await this._patch(t.id, { category_id: categoryID });
          if (!ok) {
            t.category_id = prevCategory;
            t.status = prevStatus;
            break;
          }
          okCount++;
        }
        if (okCount > 0) alert(`已应用 ${okCount} 条流水。`);
      } finally {
        this.applyingRule = false;
      }
    },

    async patchCategory(t, value) {
      const prev = t.category_id;
      t.category_id = value;
      if (value) t.status = 'confirmed';
      const ok = await this._patch(t.id, { category_id: value });
      if (!ok) t.category_id = prev;
    },

    async patchNote(t, value) {
      if (value === t.note) return;
      const prev = t.note;
      t.note = value;
      const ok = await this._patch(t.id, { note: value });
      if (!ok) t.note = prev;
    },

    async patchStatus(t, value) {
      const prev = t.status;
      t.status = value;
      const ok = await this._patch(t.id, { status: value });
      if (!ok) t.status = prev;
    },

    async patchMember(t, value) {
      value = value.trim();
      if (value === t.member) return;
      const prev = t.member;
      t.member = value;
      const ok = await this._patch(t.id, { member: value });
      if (!ok) t.member = prev;
    },

    async patchAccount(t, value) {
      const prev = t.account;
      t.account = value;
      const ok = await this._patch(t.id, { account: value });
      if (!ok) t.account = prev;
    },

    // patchSpecial 行内改专项；选到「＋ 新建专项…」就跳去 /specials 建，
    // 下拉先回滚到原值免得看起来像已经改掉了。
    async patchSpecial(t, value) {
      if (value === '__new__') {
        this.$nextTick(() => { window.location.href = '/specials'; });
        return;
      }
      if (value === t.special_id) return;
      const prev = t.special_id;
      t.special_id = value;
      const ok = await this._patch(t.id, { special_id: value });
      if (!ok) t.special_id = prev;
    },

    // applyBatchSpecial 把当前筛选出的流水一次归入（或移出）某个专项。
    // 走 PATCH /api/transactions/batch，失败整体回滚本地状态。
    async applyBatchSpecial() {
      if (this.batching || !this.batchSpecialID) return;
      const targets = this.filtered.filter(
        (t) => t.special_id !== (this.batchSpecialID === '__clear__' ? '' : this.batchSpecialID),
      );
      if (targets.length === 0) {
        alert('当前筛选的流水已经是该专项，无需处理。');
        return;
      }
      const specialID = this.batchSpecialID === '__clear__' ? '' : this.batchSpecialID;
      const label = specialID ? (this.specialNameById[specialID] || specialID) : '日常';
      if (!confirm(`把 ${targets.length} 条流水归入「${label}」？`)) return;

      this.batching = true;
      const prev = targets.map((t) => t.special_id);
      targets.forEach((t) => { t.special_id = specialID; });
      try {
        const r = await fetch('/api/transactions/batch', {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ ids: targets.map((t) => t.id), special_id: specialID }),
        });
        if (!r.ok) {
          targets.forEach((t, i) => { t.special_id = prev[i]; });
          alert('批量归类失败：' + (await r.text()));
          return;
        }
        const data = await r.json();
        alert(`已归类 ${data.updated} 条流水。`);
      } catch (e) {
        targets.forEach((t, i) => { t.special_id = prev[i]; });
        alert('批量归类失败：' + e.message);
      } finally {
        this.batching = false;
      }
    },

    async _patch(id, body) {
      try {
        const r = await fetch(`/api/transactions/${id}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!r.ok) {
          alert('保存失败：' + (await r.text()));
          return false;
        }
        return true;
      } catch (e) {
        alert('保存失败：' + e.message);
        return false;
      }
    },
  };
}
