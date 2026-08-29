// 共享的周期工具函数，供仪表盘/流水/统计三个页面的 Alpine 组件使用。

// defaultPeriodKey 返回给定粒度「上一个完整周期」的 key：当期还没走完，数字有误导性
// （环比/同比都会失真），所以三个页面的默认周期统一取上一期，而不是当期。
// 复用 shiftPeriodKey(-1) 而不是另写一套"减一"算法，避免两处进位逻辑各写一份、日后改跑偏。
function defaultPeriodKey(granularity) {
  const now = new Date();
  const y = now.getFullYear();
  const m = now.getMonth() + 1;
  const q = Math.floor((m - 1) / 3) + 1;
  const currentKey = { month: y + '-' + String(m).padStart(2, '0'), quarter: y + 'Q' + q, year: String(y) }[granularity];
  if (currentKey === undefined) return '';
  return shiftPeriodKey(granularity, currentKey, -1) || '';
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
