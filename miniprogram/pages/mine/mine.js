const store = require('../../utils/store');

Page({
  logout() {
    store.clearToken();
    wx.reLaunch({ url: '/pages/login/login' });
  },
});
