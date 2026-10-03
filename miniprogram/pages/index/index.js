const { getMeta } = require('../../utils/meta');
const request = require('../../utils/request');

Page({
  // 首页本身不展示数据，进来先拉一次 meta：既预热缓存，也借此探测登录态
  // （无 token 且服务端启用了鉴权 -> 401 -> request 层自动跳登录页）
  onShow() {
    getMeta().catch((e) => request.showError(e));
  },
  goNew() {
    wx.navigateTo({ url: '/pkg/pages/tx_new/tx_new' });
  },
  goList() {
    wx.navigateTo({ url: '/pkg/pages/tx_list/tx_list' });
  },
  goReport() {
    wx.navigateTo({ url: '/pkg/pages/report/report' });
  },
});
