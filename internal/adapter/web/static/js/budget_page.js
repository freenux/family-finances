// Alpine 组件：季度预算就地编辑（/budget）。
// SSR 嵌入 #data-budget-rows；输入即更新本地状态，保存按钮 PUT /api/budgets 整表提交。
function budgetPage() {
  const rows = JSON.parse(document.getElementById('data-budget-rows').textContent || '[]');
  const budgets = {};
  const spent = {};
  for (const r of rows) {
    budgets[r.category_id] = r.budget_fen;
    spent[r.category_id] = r.spent_fen;
  }

  return {
    budgets, // category_id -> fen
    spent,
    saving: false,
    msg: '',
    errorMsg: '',

    init() {
      this.$nextTick(() => this.fillInputs());
    },

    fillInputs() {
      document.querySelectorAll('#budget-table input.cell-input').forEach((i) => {
        const fen = this.budgets[i.dataset.cat] || 0;
        i.value = fen > 0 ? String(fen / 100) : '';
      });
    },

    onInput(cat, value) {
      const v = parseFloat(String(value).replace(/[,¥\s]/g, ''));
      this.budgets[cat] = isNaN(v) || !isFinite(v) || v <= 0 ? 0 : Math.round(v * 100);
    },

    budgetOf(cat) {
      return this.budgets[cat] || 0;
    },

    ratio(cat) {
      const b = this.budgets[cat] || 0;
      if (b <= 0) return 0;
      return (this.spent[cat] || 0) / b;
    },

    ratioLabel(cat) {
      return (this.ratio(cat) * 100).toFixed(0) + '%';
    },

    badgeClass(cat) {
      const r = this.ratio(cat);
      if (r > 1) return 'danger';
      if (r > 0.95) return 'warn';
      return 'ok';
    },

    overCount() {
      let n = 0;
      for (const cat of Object.keys(this.budgets)) {
        if (this.budgets[cat] > 0 && this.ratio(cat) > 1) n++;
      }
      return n;
    },

    fmtYuan(fen) {
      const sign = fen < 0 ? '-' : '';
      fen = Math.abs(fen);
      return `${sign}${Math.floor(fen / 100).toLocaleString('en-US')}.${String(fen % 100).padStart(2, '0')}`;
    },

    async save() {
      this.saving = true;
      this.msg = '';
      this.errorMsg = '';
      try {
        const amounts = {};
        for (const [cat, fen] of Object.entries(this.budgets)) {
          if (fen > 0) amounts[cat] = fen;
        }
        const r = await fetch('/api/budgets', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ amounts }),
        });
        if (!r.ok) throw new Error(await r.text());
        this.msg = '预算已保存。';
      } catch (e) {
        this.errorMsg = e.message;
      } finally {
        this.saving = false;
      }
    },
  };
}
