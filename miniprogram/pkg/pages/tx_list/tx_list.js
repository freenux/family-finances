const request = require('../../../utils/request');
const store = require('../../../utils/store');
const { getMeta } = require('../../../utils/meta');

const PAGE_SIZE = 20;

// 注意：本页不缓存流水列表（见 utils/store.js 的说明：Storage 10MB 上限、无索引）。
// 每次翻页/筛选/改分类后都重新向服务端取当前页。
Page({
  data: {
    rows: [], period: null, page: null, totals: null,
    directions: [], categories: [],
    direction: '', keyword: '', pickerId: '', loading: false,
  },

  onLoad() {
    const last = store.getPeriod('tx');
    // 不传 period 时由服务端给默认周期；传就必须 type+period 成对
    this._q = last ? { type: last.type, period: last.key } : {};
    this._page = 1;
    getMeta()
      .then((m) => this.setData({ directions: m.directions, categories: m.categories }))
      .catch((e) => request.showError(e));
    this.load();
  },

  load() {
    this.setData({ loading: true });
    const q = Object.assign({}, this._q, { page: this._page, page_size: PAGE_SIZE });
    if (this.data.direction) q.direction = this.data.direction;
    if (this.data.keyword) q.keyword = this.data.keyword;
    return request
      .get('/api/v1/transactions', q)
      .then((r) => {
        this._q = { type: r.period.type, period: r.period.key };
        this._page = r.page.page;
        store.setPeriod('tx', r.period);
        this.setData({ rows: r.rows, period: r.period, page: r.page, totals: r.totals });
      })
      .catch((e) => request.showError(e))
      .then(() => this.setData({ loading: false }));
  },

  // 切周期/筛选条件后回到第 1 页
  reset(extra) {
    this._page = 1;
    this.setData(extra || {}, () => this.load());
  },

  goPrev() {
    this._q = { type: this.data.period.type, period: this.data.period.prev };
    this.reset();
  },
  goNext() {
    const p = this.data.period;
    if (!p.has_next) return;
    this._q = { type: p.type, period: p.next };
    this.reset();
  },
  setDirection(e) {
    this.reset({ direction: e.currentTarget.dataset.v });
  },
  onSearch(e) {
    this.reset({ keyword: e.detail.value.trim() });
  },
  prevPage() {
    if (this.data.page.page <= 1) return;
    this._page = this.data.page.page - 1;
    this.load();
  },
  nextPage() {
    if (this.data.page.page >= this.data.page.total_pages) return;
    this._page = this.data.page.page + 1;
    this.load();
  },

  openPicker(e) {
    this.setData({ pickerId: e.currentTarget.dataset.id });
  },
  closePicker() {
    this.setData({ pickerId: '' });
  },
  noop() {},

  // 就地改分类：PATCH 后重新拉当前页，分类名/状态文案都以服务端为准，
  // 客户端不自己查表回填 category_text（也不自己把 status 改成已确认）。
  pick(e) {
    const id = this.data.pickerId;
    const categoryId = e.currentTarget.dataset.id;
    this.setData({ pickerId: '' });
    request
      .patch('/api/v1/transactions/' + encodeURIComponent(id), { category_id: categoryId })
      .then(() => this.load())
      .catch((err) => request.showError(err));
  },
});
