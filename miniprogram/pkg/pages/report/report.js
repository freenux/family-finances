const request = require('../../../utils/request');
const store = require('../../../utils/store');
const { getMeta } = require('../../../utils/meta');

Page({
  data: { type: 'quarterly', report: null, accountViews: [], account: '', loading: false },

  onLoad() {
    const last = store.getPeriod('report');
    if (last) this._query = { type: last.type, period: last.key };
    else this._query = { type: 'quarterly' }; // 不传 period = 服务端默认周期
    this.setData({ type: this._query.type });
    getMeta()
      .then((m) => this.setData({ accountViews: m.account_views }))
      .catch((e) => request.showError(e));
    this.load();
  },

  load() {
    this.setData({ loading: true });
    const q = Object.assign({}, this._query);
    if (this.data.account) q.account = this.data.account;
    return request
      .get('/api/v1/report', q)
      .then((r) => {
        // 周期原样取自响应：type 与 key 成对回传，客户端不做日期运算
        this._query = { type: r.period.type, period: r.period.key };
        store.setPeriod('report', r.period);
        this.setData({ report: r, type: r.period.type, account: r.account });
      })
      .catch((e) => request.showError(e))
      .then(() => this.setData({ loading: false }));
  },

  // 切季报/年报：只传 type，周期由服务端给默认值
  setType(e) {
    this._query = { type: e.currentTarget.dataset.type };
    this.load();
  },
  goPrev() {
    this._query = { type: this.data.report.period.type, period: this.data.report.period.prev };
    this.load();
  },
  goNext() {
    const p = this.data.report.period;
    if (!p.has_next) return; // 服务端说没有下一期就不让翻
    this._query = { type: p.type, period: p.next };
    this.load();
  },
  onAccount(e) {
    this.setData({ account: this.data.accountViews[e.detail.value].value });
    this.load();
  },
});
