const request = require('../../utils/request');
const store = require('../../utils/store');

Page({
  data: { key: '', loading: false },

  onKey(e) {
    this.setData({ key: e.detail.value });
  },

  submit() {
    if (this.data.loading) return;
    this.setData({ loading: true });
    // auth:false —— 登录接口在鉴权之外，key 错误返回的 401 不应触发「跳登录页」
    request
      .post('/api/v1/auth/token', { key: this.data.key }, { auth: false })
      .then((d) => {
        // 服务端未启用鉴权时 token 为空串，store 里存空串，之后请求不带 Authorization 头
        store.setToken(d.token);
        wx.switchTab({ url: '/pages/index/index' });
      })
      .catch((e) => request.showError(e))
      .then(() => this.setData({ loading: false }));
  },
});
