// Alpine 组件：统计页。数据来自 GET /api/stats 与 /api/stats/top，后端直接聚合真实流水。
// 依赖 period_utils.js 提供的 defaultPeriodKey / shiftPeriodKey。
// 统计口径的唯一一份定义：切换按钮的文案、头部副标题、对比条标题都从这里取。
// 顺序即按钮顺序；第一项是缺省口径（与后端 domain.ParseScope 的兜底保持一致）。
const SCOPES = [
  { key: 'daily',   label: '日常' },
  { key: 'all',     label: '全部' },
  { key: 'special', label: '仅专项' },
];

function statsPage() {
  const DAYS_IN = { month: 30, quarter: 90, year: 365 };

  return {
    granularity: 'month',
    direction:   'expense',
    account:     'family',
    // 默认日常口径：默认视图必须是干净的，一次装修就能把全局同比拉到 +368%
    scope:       'daily',
    periodKey:   defaultPeriodKey('month'),
    expandTop:   false,

    // 当前主视图（GET /api/stats 结果）
    view: null,
    loading: false,
    errorMsg: '',

    // 柱状图 focus
    focusKind: null,
    focusKey:  null,
    focusTop:  null, // 聚焦时单独 fetch 的 Top 列表
    focusing:  false,

    init() {
      this.fetchView();
    },

    // --- state 变更入口 ---
    setGranularity(g) {
      if (this.granularity === g) return;
      this.granularity = g;
      this.periodKey = defaultPeriodKey(g);
      this.resetFocus();
      this.expandTop = false;
      this.fetchView();
    },

    setDirection(d) {
      if (this.direction === d) return;
      this.direction = d;
      this.resetFocus();
      this.expandTop = false;
      this.fetchView();
    },

    setAccount(a) {
      if (this.account === a) return;
      this.account = a;
      this.resetFocus();
      this.expandTop = false;
      this.fetchView();
    },

    setScope(s) {
      if (this.scope === s) return;
      this.scope = s;
      this.resetFocus();
      this.expandTop = false;
      this.fetchView();
    },

    shiftPeriod(delta) {
      const next = shiftPeriodKey(this.granularity, this.periodKey, delta);
      if (!next) return;
      this.periodKey = next;
      this.resetFocus();
      this.expandTop = false;
      this.fetchView();
    },

    resetFocus() {
      this.focusKind = null;
      this.focusKey = null;
      this.focusTop = null;
    },

    focusBar(kind, key) {
      if (this.focusKind === kind && this.focusKey === key) {
        this.resetFocus();
        return;
      }
      this.focusKind = kind;
      this.focusKey = key;
      this.expandTop = false;
      this.fetchFocusTop();
    },

    // 双击月度/季度对比柱：把仪表盘自身的周期切到该柱代表的那一期。
    // kind 是 'month'/'quarter'，与 granularity 同一套词表；label 是后端算好的桶 label
    // （'2026-07' / '2026Q3'），跟 periodKey 格式完全一致，直接用，不用再拼一遍。
    // 单击的 focusBar 行为不变；双击会先触发两次 click（focusBar 互相抵消或短暂聚焦），
    // 但这里落地时统一 resetFocus()，最终状态只看这次调用，不受之前两次 click 影响。
    jumpToPeriod(kind, label) {
      if (this.granularity === kind && this.periodKey === label) return;
      this.granularity = kind;
      this.periodKey = label;
      this.resetFocus();
      this.expandTop = false;
      this.fetchView();
    },

    // 双击「支出构成/收入构成」的科目行：带着当前仪表盘的筛选跳到流水页。
    // 参数名与含义对齐流水页 tx_table.js 的 init()（同一套 URL 参数约定）。
    goToTransactions(categoryID) {
      const q = new URLSearchParams({
        type:     periodTypeFromGranularity(this.granularity),
        period:   this.periodKey,
        account:  this.account,
        direction: this.direction,
        category: categoryID,
      });
      // scope=all 对应流水页"全部"（不传 special，走默认），daily/special 才需要显式带上
      if (this.scope === 'daily')   q.set('special', '__none__');
      if (this.scope === 'special') q.set('special', '__any__');
      window.location.href = '/transactions?' + q.toString();
    },

    // --- 远端请求 ---
    async fetchView() {
      this.loading = true;
      this.errorMsg = '';
      const q = new URLSearchParams({
        granularity: this.granularity,
        period: this.periodKey,
        direction: this.direction,
        account: this.account,
        scope: this.scope,
      });
      try {
        const r = await fetch('/api/stats?' + q.toString());
        if (!r.ok) throw new Error('HTTP ' + r.status + ' ' + (await r.text()));
        this.view = await r.json();
      } catch (e) {
        this.errorMsg = e.message;
        this.view = null;
      } finally {
        this.loading = false;
      }
    },

    async fetchFocusTop() {
      if (!this.focusKind || !this.focusKey) return;
      this.focusing = true;
      const q = new URLSearchParams({
        period: this.focusKey,
        direction: this.direction,
        account: this.account,
        // 与 /api/stats 一样必须带口径：不带的话后端落到缺省 daily，
        // 用户切到"全部/仅专项"再点柱子，榜单还是那份日常流水
        scope: this.scope,
        limit: '10',
      });
      try {
        const r = await fetch('/api/stats/top?' + q.toString());
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.focusTop = await r.json();
      } catch (e) {
        this.errorMsg = e.message;
        this.focusTop = [];
      } finally {
        this.focusing = false;
      }
    },

    // --- 视图派生 ---
    current() { return this.view; },
    hasData() { return !!this.view && this.view.total > 0; },

    // 口径选项：切换按钮（stats.html 用 x-for 渲染）与文案（scopeLabel）共用这一份，
    // 避免"日常/全部/仅专项"在模板和 JS 里各留一份、改一处忘一处。
    scopes: SCOPES,

    // 口径文案：daily/all/special → 日常/全部/仅专项。头部副标题和对比条面板标题共用。
    scopeLabel() {
      const hit = SCOPES.find((s) => s.key === this.scope);
      return hit ? hit.label : SCOPES[0].label;
    },

    // 头部副标题
    headSubtitle() {
      const gl = { month: '月度', quarter: '季度', year: '年度' }[this.granularity];
      const ac = { husband: '男主', wife: '女主', family: '家庭总账' }[this.account];
      const di = { expense: '支出', income: '收入' }[this.direction];
      return gl + ' · ' + ac + ' · ' + di + ' · ' + this.scopeLabel() + ' · ' + this.periodKey;
    },

    perDayLabel() {
      const v = this.view;
      if (!v) return '';
      const d = DAYS_IN[this.granularity];
      return '日均 ' + this.fmtYuan(Math.round(v.total / d));
    },

    prevPeriodKey() {
      return shiftPeriodKey(this.granularity, this.periodKey, -1) || '—';
    },

    // 饼图
    pieStyle() {
      const v = this.view;
      if (!v || !v.total) return 'background: var(--zebra)';
      let acc = 0;
      const stops = v.categories.map(c => {
        const start = (acc / v.total) * 100;
        acc += c.amount;
        const end = (acc / v.total) * 100;
        return c.color + ' ' + start.toFixed(2) + '% ' + end.toFixed(2) + '%';
      });
      return 'background: conic-gradient(from -90deg, ' + stops.join(', ') + ')';
    },

    pctOf(amount) {
      const v = this.view;
      if (!v || !v.total) return '0%';
      return (amount / v.total * 100).toFixed(1) + '%';
    },

    barPct(amount) {
      const v = this.view;
      if (!v || !v.categories.length) return 0;
      const max = Math.max(...v.categories.map(x => x.amount));
      if (!max) return 0;
      return (amount / max * 100).toFixed(1);
    },

    // 对比条高度。归一基准必须和柱子上显示的数字（amount）是同一个量，否则会出现
    // "数字更大的柱子反而更矮"：amount 已经是当前口径下的完整金额（daily/all/special
    // 三选一，取决于后端 scope），不能再额外加一次 special——那是 amount 里已经含着的一截。
    barMax(arr) {
      const max = Math.max(...arr.map(x => x.amount));
      return max > 0 ? max : 0;
    },

    // 两段共用同一个归一化：实心段传 dailyPart(b)、斜纹段传 b.special，
    // 参数形状一致，改归一化语义时只有这一处，不会出现两段用上不同基准。
    normBar(amount, arr) {
      const max = this.barMax(arr);
      if (!max || !(amount > 0)) return 0;
      return (amount / max * 100).toFixed(1);
    },

    // 实心段的金额：amount 里刨掉专项之后剩下的一截（斜纹段那截是 b.special）。
    // 仅专项口径下 special === amount，这里自然是 0，模板据此不渲染实心段。
    // 日常口径下后端给的 special 恒为 null，这里就是整个 amount。
    dailyPart(b) {
      return (b.amount || 0) - (b.special || 0);
    },

    // 图例要不要显示"专项"：同样跟着后端给的 special 字段走；日常口径下每个桶的
    // special 都是 null，这里自然是 false。与柱子的渲染条件保持一致（> 0 才算）。
    hasSpecial(arr) {
      return Array.isArray(arr) && arr.some(x => x.special > 0);
    },

    focusMonth() {
      if (this.focusKind === 'month') return this.focusKey;
      return this.granularity === 'month' ? this.periodKey : null;
    },
    focusQuarter() {
      if (this.focusKind === 'quarter') return this.focusKey;
      return this.granularity === 'quarter' ? this.periodKey : null;
    },

    // Top 流水
    topSource() {
      if (this.focusKind && this.focusTop !== null) return this.focusTop;
      return this.view ? this.view.topTransactions : [];
    },
    shownTop() {
      const all = this.topSource();
      if (!all.length) return [];
      return this.expandTop ? all : all.slice(0, 5);
    },
    shownTopTotal() { return this.topSource().length; },
    topPeriodLabel() {
      if (this.focusKind && this.focusKey) return this.focusKey;
      return this.periodKey;
    },

    // 同比/环比格式化。ratio 为小数（0.12 表示 +12%），null 表基期为 0。
    // 返回 { text, cls }，cls 决定颜色：
    //   'bad' 红  — 支出上涨 / 收入下跌
    //   'good' 绿 — 支出下跌 / 收入上涨
    //   'flat' 灰、'na' 基期为 0
    fmtRatio(ratio) {
      if (ratio === null || ratio === undefined) return { text: '—', cls: 'na' };
      if (!isFinite(ratio)) return { text: '—', cls: 'na' };
      const pct = ratio * 100;
      if (Math.abs(pct) < 0.05) return { text: '0.0%', cls: 'flat' };
      const arrow = pct > 0 ? '↑' : '↓';
      // 支出：上涨=bad，下跌=good；收入反之
      const upIsBad = this.direction === 'expense';
      const cls = (pct > 0) === upIsBad ? 'bad' : 'good';
      return { text: arrow + Math.abs(pct).toFixed(1) + '%', cls };
    },

    fmtYuan(fen) {
      const sign = fen < 0 ? '-' : '';
      fen = Math.abs(fen);
      const yuan = Math.floor(fen / 100);
      const cents = fen % 100;
      return sign + yuan.toLocaleString('en-US') + '.' + String(cents).padStart(2, '0');
    },
  };
}
