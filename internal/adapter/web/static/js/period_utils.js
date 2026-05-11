// 共享的周期工具函数，供仪表盘/流水/统计三个页面的 Alpine 组件使用。

function defaultPeriodKey(granularity) {
  const now = new Date();
  const y = now.getFullYear();
  const m = now.getMonth() + 1;
  const q = Math.floor((m - 1) / 3) + 1;
  switch (granularity) {
    case 'month':   return y + '-' + String(m).padStart(2, '0');
    case 'quarter': return y + 'Q' + q;
    case 'year':    return String(y);
  }
  return '';
}

// 给定粒度 + periodKey，返回偏移后的 key；-1 = 上期
function shiftPeriodKey(granularity, key, delta) {
  if (granularity === 'year') {
    const y = parseInt(key, 10);
    if (isNaN(y)) return null;
    return String(y + delta);
  }
  if (granularity === 'quarter') {
    const m = /^(\d{4})Q([1-4])$/.exec(key);
    if (!m) return null;
    let y = parseInt(m[1], 10);
    let q = parseInt(m[2], 10) + delta;
    while (q > 4) { q -= 4; y += 1; }
    while (q < 1) { q += 4; y -= 1; }
    return y + 'Q' + q;
  }
  if (granularity === 'month') {
    const m = /^(\d{4})-(\d{1,2})$/.exec(key);
    if (!m) return null;
    let y = parseInt(m[1], 10);
    let mo = parseInt(m[2], 10) + delta;
    while (mo > 12) { mo -= 12; y += 1; }
    while (mo < 1)  { mo += 12; y -= 1; }
    return y + '-' + String(mo).padStart(2, '0');
  }
  return null;
}

// 把后端的 Period.Type（'monthly'/'quarterly'/'annual'）映射到前端 granularity（'month'/'quarter'/'year'）
function granularityFromPeriodType(t) {
  switch (t) {
    case 'monthly':   return 'month';
    case 'quarterly': return 'quarter';
    case 'annual':    return 'year';
  }
  return 'month';
}

// 反向映射：前端 granularity → 后端 type query 参数
function periodTypeFromGranularity(g) {
  switch (g) {
    case 'month':   return 'monthly';
    case 'quarter': return 'quarterly';
    case 'year':    return 'annual';
  }
  return 'monthly';
}
