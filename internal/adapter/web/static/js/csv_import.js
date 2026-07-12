// Alpine 组件：通用 CSV 映射导入（/imports/csv）。
// 上传 → POST /api/imports/csv/preview 暂存并预览 → 配置映射 → POST /imports/csv。
function csvImportPage() {
  const templates = JSON.parse(document.getElementById('data-csv-templates').textContent || '[]');
  return {
    templates,
    token: '',
    rows: [],
    totalRows: 0,
    uploading: false,
    importing: false,
    errorMsg: '',
    account: 'husband',
    member: '',
    templateName: '',
    saveTemplate: false,
    mapping: {
      skip_rows: 0,
      time_col: 0,
      date_format: '2006-01-02',
      amount_mode: 'single',
      amount_col: 1,
      debit_col: -1,
      credit_col: -1,
      desc_cols: [],
      counterparty_col: -1,
    },

    colIndexes() {
      if (!this.rows.length) return [];
      let max = 0;
      for (const r of this.rows) max = Math.max(max, r.length);
      return Array.from({ length: max }, (_, i) => i);
    },

    toggleDescCol(ci, checked) {
      const set = new Set(this.mapping.desc_cols);
      if (checked) set.add(ci); else set.delete(ci);
      this.mapping.desc_cols = [...set].sort((a, b) => a - b);
    },

    applyTemplate(name) {
      const t = this.templates.find((x) => x.name === name);
      if (!t) return;
      this.mapping = { ...this.mapping, ...t.mapping };
      this.templateName = name;
    },

    async upload(ev) {
      const file = ev.target.files[0];
      if (!file) return;
      this.uploading = true;
      this.errorMsg = '';
      try {
        const fd = new FormData();
        fd.append('file', file);
        const r = await fetch('/api/imports/csv/preview', { method: 'POST', body: fd });
        if (!r.ok) throw new Error(await r.text());
        const data = await r.json();
        this.token = data.token;
        this.rows = data.rows || [];
        this.totalRows = data.total_rows || 0;
      } catch (e) {
        this.errorMsg = e.message;
        this.rows = [];
      } finally {
        this.uploading = false;
      }
    },

    async doImport() {
      if (!this.token || this.importing) return;
      this.importing = true;
      this.errorMsg = '';
      try {
        const r = await fetch('/imports/csv', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            token: this.token,
            account: this.account,
            member: this.member,
            template_name: this.templateName,
            save_template: this.saveTemplate,
            mapping: this.mapping,
          }),
        });
        if (!r.ok) throw new Error(await r.text());
        const data = await r.json();
        window.location.href = data.redirect || '/transactions';
      } catch (e) {
        this.errorMsg = e.message;
      } finally {
        this.importing = false;
      }
    },
  };
}
