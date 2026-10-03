// ============================================================================
// 薄客户端第一原则的【唯一例外】：用户输入的元 -> 接口要求的分（amount_fen）。
//
// 接口只收整数分，不收元，所以小程序里必须换算这一次。
// 这个函数是服务端 usecase.ParseYuanToFen（internal/usecase/create_transaction.go）的孪生：
// 规则必须一致 —— 纯十进制字符串解析、不用 float、第三位小数四舍五入（19.999 -> 2000）。
// 改任何一处，必须同步改另一处。
//
// 本函数不产生错误文案：无法解析、非正数、超上限一律返回 0，
// 由服务端校验后返回「金额必须为正数」等面向用户的中文，客户端不自编提示。
// ============================================================================
function yuanToFen(input) {
  const s = String(input == null ? '' : input).trim();
  const m = /^(\d*)(?:\.(\d*))?$/.exec(s);
  if (!m || (m[1] === '' && (m[2] === undefined || m[2] === ''))) return 0;
  const intPart = m[1] || '0';
  const frac = (m[2] || '') + '000';
  // 整数部分超过 14 位肯定超上限（上限 100 亿元），直接交给服务端拒绝，同时避免丢精度
  if (intPart.length > 14) return 0;
  let fen = parseInt(intPart, 10) * 100 + parseInt(frac.slice(0, 2), 10);
  if (frac.charAt(2) >= '5') fen += 1;
  return fen;
}

module.exports = { yuanToFen };
