const request = require('../../../utils/request');
const { getMeta } = require('../../../utils/meta');

Page({
  data: {
    directions: [], accounts: [], categories: [],
    direction: '', account: '', categoryId: '', categoryName: '',
    amountYuan: '', date: '', time: '',
    counterparty: '', description: '', note: '', member: '',
    picking: false, saving: false,
  },

  onLoad() {
    // 不带 direction 的 meta：方向/账户选项来自这里；科目树等选了方向再按方向取
    getMeta()
      .then((m) => this.setData({ directions: m.directions, accounts: m.accounts, categories: m.categories }))
      .catch((e) => request.showError(e));
  },

  // 方向/账户默认不预选：不选就提交，由服务端返回「请选择收支方向」等提示
  // 切换方向：按方向重新取科目树（过滤由服务端做）；已选科目不在新树里就清掉
  setDirection(e) {
    const dir = e.currentTarget.dataset.v;
    this.setData({ direction: dir });
    getMeta(dir)
      .then((m) => {
        if (this.data.direction !== dir) return; // 期间又切换了方向，丢弃过期结果
        const stillThere = m.categories.some((g) => g.items.some((c) => c.id === this.data.categoryId));
        const patch = { categories: m.categories };
        if (!stillThere) { patch.categoryId = ''; patch.categoryName = ''; }
        this.setData(patch);
      })
      .catch((err) => request.showError(err));
  },
  setAccount(e) { this.setData({ account: e.currentTarget.dataset.v }); },
  onAmount(e) { this.setData({ amountYuan: e.detail.value }); },
  onDate(e) { this.setData({ date: e.detail.value }); },
  onTime(e) { this.setData({ time: e.detail.value }); },
  clearTime() { this.setData({ date: '', time: '' }); },
  onField(e) { this.setData({ [e.currentTarget.dataset.k]: e.detail.value }); },
  openPicker() { this.setData({ picking: true }); },
  closePicker() { this.setData({ picking: false }); },
  noop() {},
  pick(e) {
    this.setData({
      picking: false,
      categoryId: e.currentTarget.dataset.id,
      categoryName: e.currentTarget.dataset.name,
    });
  },

  submit() {
    if (this.data.saving) return;
    const d = this.data;
    this.setData({ saving: true });
    const body = {
        account: d.account,
        direction: d.direction,
        amount_yuan: d.amountYuan, // 原样发元字符串，换算与格式校验全在服务端；不要同时带 amount_fen（会 400）
        category_id: d.categoryId,
        member: d.member,
        counterparty: d.counterparty,
        description: d.description,
        note: d.note,
    };
    // 用户没选时间就不传 occurred_at，服务端取服务器当前时间；选了才传（只选一半会被服务端 400 并给出提示）
    if (d.date || d.time) body.occurred_at = d.date + ' ' + d.time;
    request
      .post('/api/v1/transactions', body)
      .then(() => {
        wx.showToast({ title: '已保存', icon: 'success' });
        // 连续记账：保留方向/账户，清空其余
        this.setData({
          amountYuan: '', categoryId: '', categoryName: '', counterparty: '',
          description: '', note: '', member: '', date: '', time: '',
        });
      })
      .catch((e) => request.showError(e))
      .then(() => this.setData({ saving: false }));
  },
});
