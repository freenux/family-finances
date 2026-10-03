// Alpine 组件：流水列表（薄客户端）。
// 只做三件事：持有筛选 / 排序 / 分页状态 → 拼 query → GET /api/v1/transactions，
// 然后把响应里的 rows / totals / page / facets / period 原样渲染。
// 筛选、排序、合计、金额与枚举的中文、分类/专项名、周期进位、规则匹配全在服务端，
// 这里不做任何推导（契约 docs/CLIENT-API-DESIGN.md §0）。
// 首屏由 SSR 把同一个 DTO 嵌进 #data-transactions，省一次请求；下拉选项来自 /api/v1/meta。
// JS 目标 ES2017：不用 ?. ?? 对象展开等。
function txTable() {
  const boot = JSON.parse(document.getElementById('data-transactions').textContent || 'null') || {};

  // 筛选器默认值的唯一来源：初始 state 与「清空筛选」共用。
  // 每次返回新数组——source / status 是 x-model 直接改的引用，共享一份会串味。
  const defaultFilters = () => ({
    keyword: '',
    direction: 'all',
    member: '',
    source: ['alipay', 'wechat', 'manual', 'csv'],
    status: ['pending_review', 'confirmed'],
    category: '',
    special: '',
  });
  const sameSet = (a, b) => a.slice().sort().join(',') === b.slice().sort().join(',');

  // 信封解包：成功 {"data":...}，失败 {"error":{"code","message"}}；失败时抛出 error.message
  async function api(method, url, body) {
    const opt = { method: method, headers: { Accept: 'application/json' } };
    if (body !== undefined) {
      opt.headers['Content-Type'] = 'application/json';
      opt.body = JSON.stringify(body);
    }
    let r;
    try {
      r = await fetch(url, opt);
    } catch (e) {
      throw new Error('网络异常，请稍后重试');
    }
    let j = null;
    try { j = await r.json(); } catch (e) { /* 非 JSON（如网关错误页） */ }
    if (!r.ok || !j || j.error) {
      throw new Error((j && j.error && j.error.message) || ('请求失败（HTTP ' + r.status + '）'));
    }
    return j.data;
  }

  return Object.assign(defaultFilters(), {
    // ---- 服务端结果（原样渲染）----
    rows: [],
    totals: { income_text: '0.00', expense_text: '0.00', net_text: '0.00', net_fen: 0 },
    pageInfo: { page: 1, page_size: 50, total: 0, total_pages: 0 },
    facets: { members: [] },
    period: { type: '', key: '', label: '', prev: '', next: '', has_next: false },
    rule: null,

    // ---- /api/v1/meta ----
    metaReady: false,
    categories: [], // 已是「分组 → 科目」的树
    specials: [],
    accounts: [],
    accountViews: [],
    statuses: [],

    // ---- 视图状态 ----
    account: 'family',
    ruleId: '',
    sortKey: 'occurred_at', // 服务端白名单里的名字
    sortOrder: 'desc',
    pageNo: 1,
    pageSize: 50,
    loading: false,
    errorMsg: '',
    selected: [], // 勾选的流水 id（只在当页）
    batchSpecialID: '',
    batching: false,
    applyingRule: false,
    _timer: null,
    _seq: 0,

    init() {
      const el = this.$el;
      this.account = el.dataset.initialAccount || 'family';
      this.applyResult(boot);
      // URL 上的筛选参数本来就是 API 参数（仪表盘穿透、分享链接、刷新），原样读回来即可
      const q = new URLSearchParams(window.location.search);
      const f = {};
      ['keyword', 'direction', 'category', 'special', 'member'].forEach((k) => { if (q.has(k)) f[k] = q.get(k); });
      if (q.get('source')) f.source = q.get('source').split(',');
      if (q.get('status')) f.status = q.get('status').split(',');
      Object.assign(this, f);
      this.ruleId = q.get('rule_id') || '';
      this.sortKey = q.get('sort') || 'occurred_at';
      this.sortOrder = q.get('order') || 'desc';

      // 筛选 / 排序 / 每页条数一变就回第 1 页重查（同一个定时器合并多字段的连续变化；关键词稍作防抖）
      ['direction', 'member', 'source', 'status', 'category', 'special', 'account', 'sortKey', 'sortOrder', 'pageSize']
        .forEach((k) => this.$watch(k, () => this.refilter(0)));
      this.$watch('keyword', () => this.refilter(300));

      this.loadMeta();
    },

    async loadMeta() {
      try {
        const m = await api('GET', '/api/v1/meta');
        this.categories = m.categories;
        this.specials = m.specials;
        this.accounts = m.accounts;
        this.accountViews = m.account_views;
        this.statuses = m.statuses;
        this.metaReady = true;
      } catch (e) {
        this.errorMsg = '加载下拉选项失败：' + e.message;
      }
    },

    // ---- 拼 query / 同步 URL ----
    // 这些参数和 /api/v1/transactions 的入参一一对应，同一份既用来请求也用来写地址栏。
    query() {
      const q = new URLSearchParams();
      if (this.period.type) q.set('type', this.period.type);
      if (this.period.key) q.set('period', this.period.key);
      q.set('account', this.account);
      if (this.direction !== 'all') q.set('direction', this.direction);
      if (!sameSet(this.source, defaultFilters().source)) q.set('source', this.source.join(','));
      if (!sameSet(this.status, defaultFilters().status)) q.set('status', this.status.join(','));
      ['category', 'special', 'member'].forEach((k) => { if (this[k]) q.set(k, this[k]); });
      if (this.keyword.trim()) q.set('keyword', this.keyword.trim());
      if (this.ruleId) q.set('rule_id', this.ruleId);
      if (this.sortKey !== 'occurred_at' || this.sortOrder !== 'desc') {
        q.set('sort', this.sortKey);
        q.set('order', this.sortOrder);
      }
      if (this.pageNo > 1) q.set('page', String(this.pageNo));
      if (this.pageSize !== 50) q.set('page_size', String(this.pageSize));
      return q;
    },

    syncURL() {
      const s = this.query().toString();
      window.history.replaceState(null, '', window.location.pathname + (s ? '?' + s : ''));
    },

    applyResult(d) {
      this.rows = d.rows || [];
      this.totals = d.totals || this.totals;
      this.pageInfo = d.page || this.pageInfo;
      this.facets = d.facets || { members: [] };
      this.period = d.period || this.period;
      this.rule = d.rule || null;
      this.pageNo = this.pageInfo.page;
      this.pageSize = this.pageInfo.page_size; // 服务端钳制后的实际值
    },

    // silent：编辑后的静默刷新，不闪「加载中」、不清勾选
    async load(silent) {
      const seq = ++this._seq;
      if (!silent) { this.loading = true; this.selected = []; }
      this.errorMsg = '';
      this.syncURL();
      try {
        const d = await api('GET', '/api/v1/transactions?' + this.query().toString());
        if (seq !== this._seq) return; // 已有更新的请求在路上，丢弃这次的结果
        this.applyResult(d);
        if (silent) {
          const ids = {};
          this.rows.forEach((t) => { ids[t.id] = true; });
          this.selected = this.selected.filter((id) => ids[id]);
        }
        // 翻页期间数据变少导致落在末页之后：回到最后一页
        if (this.rows.length === 0 && this.pageInfo.total > 0 && this.pageInfo.total_pages > 0
            && this.pageNo !== this.pageInfo.total_pages) {
          this.pageNo = this.pageInfo.total_pages;
          return this.load(silent);
        }
      } catch (e) {
        if (seq === this._seq) this.errorMsg = e.message;
      } finally {
        if (seq === this._seq) this.loading = false;
      }
    },

    refilter(delay) {
      clearTimeout(this._timer);
      this.pageNo = 1;
      this._timer = setTimeout(() => this.load(), delay || 0);
    },

    // ---- 周期：只用服务端给的 prev / next / has_next ----
    shiftPeriod(delta) {
      const key = delta < 0 ? this.period.prev : (this.period.has_next ? this.period.next : '');
      if (!key) return;
      this.period = Object.assign({}, this.period, { key: key });
      this.pageNo = 1;
      this.load();
    },

    // 换粒度：只传 type 不传 period，让服务端给该粒度的默认周期
    setPeriodType(t) {
      if (this.period.type === t) return;
      this.period = Object.assign({}, this.period, { type: t, key: '' });
      this.pageNo = 1;
      this.load();
    },

    goPage(n) {
      if (n < 1 || (this.pageInfo.total_pages && n > this.pageInfo.total_pages) || n === this.pageNo) return;
      this.pageNo = n;
      this.load();
    },

    // ---- 排序：点列头（PC）与排序下拉（手机）都只是改 sortKey / sortOrder ----
    sortBy(key) {
      if (this.sortKey === key) {
        this.sortOrder = this.sortOrder === 'asc' ? 'desc' : 'asc';
      } else {
        this.sortKey = key;
        this.sortOrder = 'desc';
      }
    },

    sortIcon(key) {
      if (this.sortKey !== key) return '↕';
      return this.sortOrder === 'asc' ? '↑' : '↓';
    },

    resetFilters() {
      Object.assign(this, defaultFilters());
      this.ruleId = ''; // 规则预筛也是筛选条件之一，清空时一起去掉
      this.refilter(0);
    },

    // 带异步选项的筛选下拉：x-model 先于选项渲染，值落空会停在第一项。
    // x-effect 里把 deps（meta / facets 加载状态）传进来以便重新同步，等选项就位后再回填。
    syncSelect(el, value) {
      this.$nextTick(() => { el.value = value; });
    },

    // ---- 勾选 ----
    allSelected() {
      return this.rows.length > 0 && this.selected.length === this.rows.length;
    },
    toggleAll(checked) {
      this.selected = checked ? this.rows.map((t) => t.id) : [];
    },

    // ---- 规则批量应用：一次 POST，服务端单事务 ----
    async applyRule() {
      if (!this.rule || this.applyingRule) return;
      this.applyingRule = true;
      try {
        const d = await api('POST', '/api/v1/rules/' + encodeURIComponent(this.rule.id) + '/apply', {
          type: this.period.type, period: this.period.key, account: this.account,
        });
        alert('已应用 ' + d.updated + ' 条流水。');
        await this.load(true);
      } catch (e) {
        alert('应用规则失败：' + e.message);
      } finally {
        this.applyingRule = false;
      }
    },

    // ---- 就地编辑：乐观更新，失败回滚；成功后静默重查（编辑可能改变筛选 / 排序 / 合计）----
    async patchCategory(t, value) {
      const prev = t.category_id;
      t.category_id = value;
      if (value) t.status = 'confirmed';
      const ok = await this._patch(t.id, { category_id: value });
      if (!ok) t.category_id = prev;
    },

    async patchNote(t, value) {
      if (value === t.note) return;
      const prev = t.note;
      t.note = value;
      const ok = await this._patch(t.id, { note: value }, true);
      if (!ok) t.note = prev;
    },

    async patchStatus(t, value) {
      const prev = t.status;
      t.status = value;
      const ok = await this._patch(t.id, { status: value });
      if (!ok) t.status = prev;
    },

    async patchMember(t, value) {
      value = value.trim();
      if (value === t.member) return;
      const prev = t.member;
      t.member = value;
      const ok = await this._patch(t.id, { member: value });
      if (!ok) t.member = prev;
    },

    async patchAccount(t, value) {
      const prev = t.account;
      t.account = value;
      const ok = await this._patch(t.id, { account: value });
      if (!ok) t.account = prev;
    },

    // patchSpecial 收 <select> 元素本身，因为 __new__ 分支要写回它的值：
    // 选到「＋ 新建专项…」就跳去 /specials 建，跳转前必须把下拉复位到这条流水的真实归属，
    // 否则浏览器后退（含 bfcache）回来时下拉会停在「＋ 新建专项…」。
    async patchSpecial(t, el) {
      const value = el.value;
      if (value === '__new__') {
        el.value = t.special_id || '';
        window.location.href = '/specials';
        return;
      }
      if (value === t.special_id) return;
      const prev = t.special_id;
      t.special_id = value;
      const ok = await this._patch(t.id, { special_id: value });
      if (!ok) t.special_id = prev;
    },

    // 把勾选的流水一次归入（或移出）某个专项：PATCH /api/v1/transactions/batch，失败整体回滚本地状态。
    async applyBatchSpecial() {
      if (this.batching || !this.batchSpecialID || this.selected.length === 0) return;
      const specialID = this.batchSpecialID === '__clear__' ? '' : this.batchSpecialID;
      const sel = this.$refs.batchSpecial;
      const label = specialID ? sel.options[sel.selectedIndex].text : '日常';
      const ids = this.selected.slice();
      if (!confirm('把勾选的 ' + ids.length + ' 条流水归入「' + label + '」？')) return;

      this.batching = true;
      const targets = this.rows.filter((t) => ids.indexOf(t.id) >= 0);
      const prev = targets.map((t) => t.special_id);
      targets.forEach((t) => { t.special_id = specialID; });
      try {
        const d = await api('PATCH', '/api/v1/transactions/batch', { ids: ids, special_id: specialID });
        alert('已归类 ' + d.updated + ' 条流水。');
        this.selected = [];
        await this.load(true);
      } catch (e) {
        targets.forEach((t, i) => { t.special_id = prev[i]; });
        alert('批量归类失败：' + e.message);
      } finally {
        this.batching = false;
      }
    },

    async _patch(id, body, skipReload) {
      try {
        await api('PATCH', '/api/v1/transactions/' + encodeURIComponent(id), body);
      } catch (e) {
        alert('保存失败：' + e.message);
        return false;
      }
      if (!skipReload) this.load(true);
      return true;
    },
  });
}
