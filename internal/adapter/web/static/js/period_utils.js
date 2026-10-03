// 周期工具函数。
//
// 过渡期说明：周期规则（默认周期、上下期进位）的唯一来源是服务端 usecase.PeriodNav。
// 流水页（tx_table.js）已切到服务端，只消费响应里的 period.prev / next / has_next，不再依赖本文件。
// 下面的 defaultPeriodKey / shiftPeriodKey 是前端的另一份实现，目前仅剩 dashboard_page.js 与
// stats_page.js 两个页面在用（资产页 assets.js 并未引用）——待它们切到 /api/v1 后删除这两个函数。
// 在那之前改周期规则必须两处同步，见 CLAUDE.md「默认周期」一节。
// granularityFromPeriodType / periodTypeFromGranularity 是纯词表映射，可长期保留。

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
