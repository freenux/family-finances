// Alpine 组件：流水列表的排序 / 筛选 / 汇总 / 就地编辑
function txTable() {
  const txData = JSON.parse(document.getElementById('data-transactions').textContent || '[]');
  const catData = JSON.parse(document.getElementById('data-categories').textContent || '[]');

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

    keyword: '',
    directionFilter: 'all',
    sourceFilter: ['alipay', 'wechat', 'manual'],
    statusFilter: ['pending_review', 'confirmed'],
    categoryFilter: '',

    sortKey: 'occurred_at',
    sortDir: 'desc', // 'asc' | 'desc' | null

    get filtered() {
      const kw = this.keyword.trim().toLowerCase();
      const dir = this.directionFilter;
      const sources = new Set(this.sourceFilter);
      const statuses = new Set(this.statusFilter);
      const catFilter = this.categoryFilter;

      let out = this.rows.filter((t) => {
        if (dir !== 'all' && t.direction !== dir) return false;
        if (!sources.has(t.source)) return false;
        if (!statuses.has(t.status)) return false;
        if (catFilter === '__none__') {
          if (t.category_id) return false;
        } else if (catFilter && t.category_id !== catFilter) {
          return false;
        }
        if (kw) {
          const hay = (
            (t.counterparty || '') + ' ' +
            (t.description || '') + ' ' +
            (t.note || '')
          ).toLowerCase();
          if (!hay.includes(kw)) return false;
        }
        return true;
      });

      const key = this.sortKey;
      const sign = this.sortDir === 'asc' ? 1 : -1;
      out.sort((a, b) => {
        let av = a[key], bv = b[key];
        if (key === 'category_id') {
          av = catNameById[av] || '';
          bv = catNameById[bv] || '';
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
      this.sourceFilter = ['alipay', 'wechat', 'manual'];
      this.statusFilter = ['pending_review', 'confirmed'];
      this.categoryFilter = '';
    },

    async patchCategory(t, value) {
      const prev = t.category_id;
      t.category_id = value;
      // 命中分类时，后端会自动把 pending_review 转 confirmed
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

    async _patch(id, body) {
      try {
        const r = await fetch(`/transactions/${id}`, {
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
