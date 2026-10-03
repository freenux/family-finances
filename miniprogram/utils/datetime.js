// 手填记账「默认现在」：把当前本地时间拼成服务端推荐的 "YYYY-MM-DD HH:mm"。
// 这只是格式拼接，不是业务推导；服务端 ParseOccurredAt 负责校验。
function pad(n) {
  return n < 10 ? '0' + n : '' + n;
}
function nowParts() {
  const d = new Date();
  return {
    date: d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()),
    time: pad(d.getHours()) + ':' + pad(d.getMinutes()),
  };
}
module.exports = { nowParts };
