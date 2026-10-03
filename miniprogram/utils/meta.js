// /api/v1/meta 只在内存里缓存一次（模块变量，小程序退出即丢），不写 Storage。
// 内容是科目树、专项、账户等下拉选项，全部由服务端装配好，这里不做任何整形。
const request = require('./request');

let cached = null;

function getMeta() {
  if (!cached) {
    cached = request.get('/api/v1/meta').catch((e) => {
      cached = null; // 失败不要把失败的 promise 缓存住
      throw e;
    });
  }
  return cached;
}

module.exports = { getMeta };
