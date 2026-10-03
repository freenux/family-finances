const request = require('../../../utils/request');
const { getMeta } = require('../../../utils/meta');
const { yuanToFen } = require('../../../utils/amount');
const { nowParts } = require('../../../utils/datetime');

Page({
  data: {
    directions: [], accounts: [], categories: [],
    direction: '', account: '', categoryId: '', categoryName: '',
    amountYuan: '', date: '', time: '',
    counterparty: '', description: '', note: '', member: '',
    picking: false, saving: false,
  },

  onLoad() {
    const n = nowParts();
    this.setData({ date: n.date, time: n.time });
    getMeta()
      .then((m) => this.setData({ directions: m.directions, accounts: m.accounts, categories: m.categories }))
      .catch((e) => request.showError(e));
  },

  // 方向/账户默认不预选：不选就提交，由服务端返回「请选择收支方向」等提示
  setDirection(e) { this.setData({ direction: e.currentTarget.dataset.v }); },
  setAccount(e) { this.setData({ account: e.currentTarget.dataset.v }); },
  onAmount(e) { this.setData({ amountYuan: e.detail.value }); },
  onDate(e) { this.setData({ date: e.detail.value }); },
  onTime(e) { this.setData({ time: e.detail.value }); },
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
    request
      .post('/api/v1/transactions', {
        occurred_at: d.date + ' ' + d.time,
        account: d.account,
        direction: d.direction,
        amount_fen: yuanToFen(d.amountYuan), // 唯一的客户端换算，见 utils/amount.js
        category_id: d.categoryId,
        member: d.member,
        counterparty: d.counterparty,
        description: d.description,
        note: d.note,
      })
      .then(() => {
        wx.showToast({ title: '已保存', icon: 'success' });
        const n = nowParts();
        // 连续记账：保留方向/账户，清空其余
        this.setData({
          amountYuan: '', categoryId: '', categoryName: '', counterparty: '',
          description: '', note: '', member: '', date: n.date, time: n.time,
        });
      })
      .catch((e) => request.showError(e))
      .then(() => this.setData({ saving: false }));
  },
});
