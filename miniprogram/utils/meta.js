// /api/v1/meta 只在内存里缓存（模块变量，小程序退出即丢），不写 Storage。
// 内容是科目树、专项、账户等下拉选项，全部由服务端装配好，这里不做任何整形。
// 按 direction 分别缓存：'' / income / expense 各一份，互不覆盖。
// direction 只是透传给服务端的查询参数，「哪些科目属于哪个方向」由服务端决定。
const request = require('./request');

const cache = {};

function getMeta(direction) {
  const key = direction || '';
  if (!cache[key]) {
    const q = key ? { direction: key } : undefined;
    cache[key] = request.get('/api/v1/meta', q).catch((e) => {
      delete cache[key]; // 失败不要把失败的 promise 缓存住
      throw e;
    });
  }
  return cache[key];
}

module.exports = { getMeta };
