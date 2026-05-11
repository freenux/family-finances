// Alpine 组件：仪表盘。切换周期时走 GET /partials/report 拿 HTML 片段，innerHTML 替换 #report-container。
function dashboardPage() {
  return {
    granularity: 'month',
    periodKey:   '',
    account:     'family',
    loading:     false,
    errorMsg:    '',

    init() {
      const el = this.$el;
      const g = el.dataset.initialGranularity || 'monthly';
      this.granularity = granularityFromPeriodType(g);
      this.periodKey = el.dataset.initialPeriod || defaultPeriodKey(this.granularity);
      this.account = el.dataset.initialAccount || 'family';
    },

    get pageTitle() {
      return this.periodKey + ' 家庭财报';
    },

    setGranularity(g) {
      if (this.granularity === g) return;
      this.granularity = g;
      this.periodKey = defaultPeriodKey(g);
      this.refreshReport();
    },

    setAccount(a) {
      if (this.account === a) return;
      this.account = a;
      this.refreshReport();
    },

    shiftPeriod(delta) {
      const next = shiftPeriodKey(this.granularity, this.periodKey, delta);
      if (!next) return;
      this.periodKey = next;
      this.refreshReport();
    },

    async refreshReport() {
      this.loading = true;
      this.errorMsg = '';
      const q = new URLSearchParams({
        type:    periodTypeFromGranularity(this.granularity),
        period:  this.periodKey,
        account: this.account,
      });
      this.syncURL(q);
      try {
        const r = await fetch('/partials/report?' + q.toString());
        if (!r.ok) throw new Error('HTTP ' + r.status + ' ' + (await r.text()));
        const html = await r.text();
        const container = document.getElementById('report-container');
        if (container) container.innerHTML = html;
      } catch (e) {
        this.errorMsg = e.message;
      } finally {
        this.loading = false;
      }
    },

    syncURL(q) {
      const url = window.location.pathname + '?' + q.toString();
      window.history.replaceState(null, '', url);
    },
  };
}
