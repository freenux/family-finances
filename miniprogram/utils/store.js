// 本地存储封装。
//
// 只存两样东西：token 和「上次选的周期」。
// 绝对不要在这里缓存流水列表：wx.setStorage 总上限 10MB、单 key 1MB，
// 而且只是 key-value，没有索引，没法按日期/分类/关键词查询；流水量一大必然装不下，
// 装得下也只能整块反序列化后遍历，不如每次向服务端分页查询。
// 流水的筛选、排序、分页、合计全部由服务端负责。
const TOKEN_KEY = 'token';
const PERIOD_KEY = 'last_period';

function getToken() {
  try {
    return wx.getStorageSync(TOKEN_KEY) || '';
  } catch (e) {
    return '';
  }
}
function setToken(t) {
  try {
    wx.setStorageSync(TOKEN_KEY, t || '');
  } catch (e) {}
}
function clearToken() {
  try {
    wx.removeStorageSync(TOKEN_KEY);
  } catch (e) {}
}

// scope: 'tx' | 'report'，各页分开记；值是服务端响应里的 period 对象的 {type, key}，原样回传
function getPeriod(scope) {
  try {
    const all = wx.getStorageSync(PERIOD_KEY) || {};
    return all[scope] || null;
  } catch (e) {
    return null;
  }
}
function setPeriod(scope, period) {
  try {
    const all = wx.getStorageSync(PERIOD_KEY) || {};
    all[scope] = { type: period.type, key: period.key };
    wx.setStorageSync(PERIOD_KEY, all);
  } catch (e) {}
}

module.exports = { getToken, setToken, clearToken, getPeriod, setPeriod };
