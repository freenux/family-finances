// Alpine 组件：资产快照页（/assets）。
// SSR 嵌入 #data-asset-data（code→分）与 #data-asset-curve（净值序列），
// 就地编辑重算合计，保存按钮 PUT /api/assets/{period}，右侧 SVG 净值曲线带 hover tooltip。
function assetsPage() {
  const data = JSON.parse(document.getElementById('data-asset-data').textContent || '{}');
  const curve = JSON.parse(document.getElementById('data-asset-curve').textContent || '[]');

  return {
    period: '',
    prevPeriod: '',
    prevNetWorth: null, // 分；null = 上季无快照
    data,               // code -> 分
    curve,              // [{period, net_worth}]
    saving: false,
    msg: '',
    errorMsg: '',

    init() {
      const el = this.$el;
      this.period = el.dataset.period || '';
      this.prevPeriod = el.dataset.prevPeriod || '';
      if (el.dataset.hasPrev === '1') {
        this.prevNetWorth = parseInt(el.dataset.prevNetWorth, 10) || 0;
      }
      this.fillInputs();
      this.$nextTick(() => this.drawCurve());
    },

    // ---- 金额 ----

    fmtYuan(fen) {
      const sign = fen < 0 ? '-' : '';
      fen = Math.abs(fen);
      const yuan = Math.floor(fen / 100);
      const cents = fen % 100;
      return `${sign}${yuan.toLocaleString('en-US')}.${String(cents).padStart(2, '0')}`;
    },

    // 输入框展示值：整数元不带小数，带分则保留两位
    inputStr(code) {
      const fen = this.data[code] || 0;
      if (fen % 100 === 0) return (fen / 100).toLocaleString('en-US');
      return (fen / 100).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
    },

    parseYuan(value) {
      const v = parseFloat(String(value).replace(/[,¥\s]/g, ''));
      if (isNaN(v) || !isFinite(v) || v < 0) return 0;
      return Math.round(v * 100);
    },

    fillInputs() {
      document.querySelectorAll('#snap-table input.cell-input').forEach((i) => {
        i.value = this.inputStr(i.dataset.code);
      });
    },

    onInput(code, value) {
      this.data[code] = this.parseYuan(value);
      this.syncCurvePoint();
      this.drawCurve();
    },

    reformat(el, code) {
      el.value = this.inputStr(code);
    },

    // ---- 合计 ----

    get totalAssets() {
      let sum = 0;
      for (const [code, v] of Object.entries(this.data)) {
        if (code.startsWith('asset.')) sum += v;
      }
      return sum;
    },

    get totalLiab() {
      let sum = 0;
      for (const [code, v] of Object.entries(this.data)) {
        if (code.startsWith('liability.')) sum += v;
      }
      return sum;
    },

    get netWorth() {
      return this.totalAssets - this.totalLiab;
    },

    get qoqPct() {
      if (this.prevNetWorth === null || this.prevNetWorth === 0) return null;
      return (this.netWorth - this.prevNetWorth) / Math.abs(this.prevNetWorth);
    },

    // 编辑时让曲线上当期点实时联动（未保存也先预览）
    syncCurvePoint() {
      const idx = this.curve.findIndex((p) => p.period === this.period);
      if (idx >= 0) {
        this.curve[idx].net_worth = this.netWorth;
      } else {
        this.curve.push({ period: this.period, net_worth: this.netWorth });
        this.curve.sort((a, b) => a.period.localeCompare(b.period));
      }
    },

    // ---- 保存 / 从上季复制 ----

    async save() {
      this.saving = true;
      this.msg = '';
      this.errorMsg = '';
      try {
        const r = await fetch(`/api/assets/${this.period}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ data: this.data }),
        });
        if (!r.ok) throw new Error(await r.text());
        const res = await r.json();
        this.syncCurvePoint();
        this.drawCurve();
        this.msg = `${this.period} 快照已保存，净资产 ¥${this.fmtYuan(res.net_worth)}。`;
      } catch (e) {
        this.errorMsg = e.message;
      } finally {
        this.saving = false;
      }
    },

    async copyPrev() {
      this.msg = '';
      this.errorMsg = '';
      try {
        const r = await fetch(`/api/assets/${this.period}/prev`);
        if (!r.ok) throw new Error(await r.text());
        const res = await r.json();
        if (!res.found) {
          this.errorMsg = `${res.period} 暂无快照，无法复制`;
          return;
        }
        this.data = { ...res.data };
        this.fillInputs();
        this.syncCurvePoint();
        this.drawCurve();
        this.msg = `已复制 ${res.period} 余额，可在此基础上微调后保存。`;
      } catch (e) {
        this.errorMsg = e.message;
      }
    },

    // ---- 净值 SVG 曲线：面积 + 折线 + 端点 + hover ----

    drawCurve() {
      const svg = document.getElementById('nw-chart');
      if (!svg || this.curve.length === 0) return;
      const NW = { W: 520, H: 230, pl: 64, pr: 16, pt: 18, pb: 30 };
      const pts = this.curve;
      const { x, y, lo, hi } = this.scale(NW);

      let g = '';
      const steps = 4;
      for (let i = 0; i <= steps; i++) {
        const v = lo + ((hi - lo) * i) / steps;
        const yy = y(v);
        g += `<line x1="${NW.pl}" y1="${yy}" x2="${NW.W - NW.pr}" y2="${yy}" stroke="#e3e8ee" stroke-width="1"/>`;
        g += `<text x="${NW.pl - 8}" y="${yy + 4}" text-anchor="end" font-size="10" fill="#64748d">${(v / 1000000).toFixed(0)}万</text>`;
      }
      pts.forEach((p, i) => {
        if (i % 2 === 0 || i === pts.length - 1) {
          g += `<text x="${x(i)}" y="${NW.H - 10}" text-anchor="middle" font-size="10" fill="#64748d">${p.period.replace('20', '')}</text>`;
        }
      });
      const linePts = pts.map((p, i) => `${x(i)},${y(p.net_worth)}`).join(' ');
      if (pts.length > 1) {
        const area =
          `M ${x(0)},${y(pts[0].net_worth)} ` +
          pts.map((p, i) => `L ${x(i)},${y(p.net_worth)}`).join(' ') +
          ` L ${x(pts.length - 1)},${NW.H - NW.pb} L ${x(0)},${NW.H - NW.pb} Z`;
        g += `<defs><linearGradient id="nwg" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stop-color="#2a78d6" stop-opacity=".18"/><stop offset="1" stop-color="#2a78d6" stop-opacity="0"/></linearGradient></defs>`;
        g += `<path d="${area}" fill="url(#nwg)"/>`;
        g += `<polyline points="${linePts}" fill="none" stroke="#2a78d6" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`;
      }
      const last = pts.length - 1;
      g += `<circle cx="${x(last)}" cy="${y(pts[last].net_worth)}" r="4.5" fill="#2a78d6" stroke="#fff" stroke-width="2"/>`;
      g += `<text x="${x(last) - 6}" y="${y(pts[last].net_worth) - 10}" text-anchor="end" font-size="11" font-weight="600" fill="#0d253d">${(pts[last].net_worth / 1000000).toFixed(0)}万</text>`;
      g += `<line id="nw-cross" x1="0" y1="${NW.pt}" x2="0" y2="${NW.H - NW.pb}" stroke="#a8c3de" stroke-width="1" stroke-dasharray="3 3" opacity="0"/>`;
      g += `<circle id="nw-dot" r="4" fill="#2a78d6" stroke="#fff" stroke-width="2" opacity="0"/>`;
      g += `<rect x="${NW.pl}" y="${NW.pt}" width="${NW.W - NW.pl - NW.pr}" height="${NW.H - NW.pt - NW.pb}" fill="transparent" id="nw-hit"/>`;
      svg.innerHTML = g;
      this.bindHover(NW);
    },

    scale(NW) {
      const vs = this.curve.map((p) => p.net_worth);
      let min = Math.min(...vs);
      let max = Math.max(...vs);
      if (min === max) {
        // 单点或水平线：给出一个合理的显示区间
        min -= 1000000;
        max += 1000000;
      }
      const unit = 5000000; // 5 万元（分）对齐
      const lo = Math.floor((min * 0.985) / unit) * unit;
      const hi = Math.ceil((max * 1.01) / unit) * unit;
      const n = this.curve.length;
      const x = (i) => (n === 1 ? (NW.pl + NW.W - NW.pr) / 2 : NW.pl + ((NW.W - NW.pl - NW.pr) * i) / (n - 1));
      const y = (v) => NW.pt + (NW.H - NW.pt - NW.pb) * (1 - (v - lo) / (hi - lo));
      return { lo, hi, x, y };
    },

    bindHover(NW) {
      const svg = document.getElementById('nw-chart');
      const hit = document.getElementById('nw-hit');
      const tip = document.getElementById('nw-tip');
      const wrap = document.getElementById('nw-chart-wrap');
      if (!hit) return;
      const { x, y } = this.scale(NW);
      const pts = this.curve;
      const move = (e) => {
        const r = svg.getBoundingClientRect();
        const sx = ((e.clientX - r.left) * NW.W) / r.width;
        let best = 0, bd = Infinity;
        pts.forEach((p, i) => {
          const d = Math.abs(x(i) - sx);
          if (d < bd) { bd = d; best = i; }
        });
        const p = pts[best], px = x(best), py = y(p.net_worth);
        const cross = document.getElementById('nw-cross');
        cross.setAttribute('x1', px); cross.setAttribute('x2', px); cross.setAttribute('opacity', 1);
        const dot = document.getElementById('nw-dot');
        dot.setAttribute('cx', px); dot.setAttribute('cy', py); dot.setAttribute('opacity', 1);
        const prev = pts[best - 1];
        const delta = prev && prev.net_worth !== 0 ? ((p.net_worth - prev.net_worth) / Math.abs(prev.net_worth)) * 100 : null;
        tip.innerHTML = `${p.period} 净资产 <b>¥${this.fmtYuan(p.net_worth)}</b>` +
          (delta !== null ? `<br>环比 <b>${delta >= 0 ? '+' : ''}${delta.toFixed(1)}%</b>` : '');
        const wr = wrap.getBoundingClientRect();
        tip.style.left = (r.left - wr.left + (px * r.width) / NW.W) + 'px';
        tip.style.top = (r.top - wr.top + (py * r.height) / NW.H) + 'px';
        tip.style.opacity = 1;
      };
      const leave = () => {
        tip.style.opacity = 0;
        document.getElementById('nw-cross').setAttribute('opacity', 0);
        document.getElementById('nw-dot').setAttribute('opacity', 0);
      };
      hit.addEventListener('mousemove', move);
      hit.addEventListener('mouseleave', leave);
    },
  };
}
