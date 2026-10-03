// 统一请求层：注入 Bearer、解信封、按 error.code 分支。
// 约定见 docs/CLIENT-API-DESIGN.md §2 §7。
const config = require('../config');
const store = require('./store');

const LOGIN_URL = '/pages/login/login';
let redirecting = false;

// 失败统一抛这个对象：code / message 来自服务端信封，message 是服务端写好的中文，原样展示。
function apiError(code, message, status) {
  return { code, message, status, handled: false };
}

function toLogin() {
  store.clearToken();
  if (redirecting) return;
  redirecting = true;
  wx.reLaunch({
    url: LOGIN_URL,
    complete() {
      redirecting = false;
    },
  });
}

/**
 * @param {string} method GET|POST|PATCH
 * @param {string} path   形如 /api/v1/meta
 * @param {object} opts   { data, auth: false 表示不带 token 且 401 不跳登录（登录接口用） }
 * @returns {Promise<any>} 成功时是信封里的 data
 */
function request(method, path, opts) {
  const o = opts || {};
  const header = { 'content-type': 'application/json' };
  const token = store.getToken();
  // 鉴权未启用时服务端发空 token，此时不带 Authorization 头也能访问
  if (o.auth !== false && token) header.Authorization = 'Bearer ' + token;

  return new Promise((resolve, reject) => {
    wx.request({
      url: config.API_BASE + path,
      method,
      data: o.data,
      header,
      timeout: 15000,
      success(res) {
        const body = res.data;
        if (res.statusCode >= 200 && res.statusCode < 300 && body && body.error === undefined) {
          resolve(body.data);
          return;
        }
        const e = body && body.error;
        const err = apiError(
          (e && e.code) || 'internal',
          (e && e.message) || '服务暂时不可用，请稍后重试',
          res.statusCode
        );
        if (err.code === 'unauthorized' && o.auth !== false) {
          toLogin();
          err.handled = true;
        }
        reject(err);
      },
      fail() {
        reject(apiError('network', '网络连接失败，请检查网络后重试', 0));
      },
    });
  });
}

// 展示错误：message 原样给用户。unauthorized 已跳登录，不再弹窗。
// too_many_requests 用模态框而不是 toast，保证用户读完「请 15 分钟后再试」。
function showError(err) {
  if (!err || err.handled) return;
  const msg = err.message || '操作失败';
  if (err.code === 'too_many_requests') {
    wx.showModal({ title: '请稍后重试', content: msg, showCancel: false });
    return;
  }
  wx.showToast({ title: msg, icon: 'none', duration: 2500 });
}

module.exports = {
  get: (path, data, opts) => request('GET', path, Object.assign({ data }, opts)),
  post: (path, data, opts) => request('POST', path, Object.assign({ data }, opts)),
  patch: (path, data, opts) => request('PATCH', path, Object.assign({ data }, opts)),
  showError,
};
