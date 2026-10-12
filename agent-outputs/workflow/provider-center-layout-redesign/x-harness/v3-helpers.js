// v3 浏览器侧助手：只点真实按钮、只读 DOM，不改 Vue 状态。
// v3 信息架构：二三级回到顶侧 .pc-subnav（v1 形态）；页面里唯一的 SectionNav 是编辑器内部的 local 分区导航。
(() => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  const LOCAL_NAV = 'nav.section-nav[aria-label="配置分区"]';
  const EDITOR_ROOTS = '.pac-root,.ogc-root,.pre-root,.ace-root,.oc-global';
  // 视图 → [一级, 二级, （可选）三级]；文案是被测对象，不从生产常量导入（测试预言机独立持有）。
  const VIEW = {
    'presets-anthropic': ['预设', 'Anthropic 格式'], 'presets-openai': ['预设', 'OpenAI 格式'],
    'pi-agents': ['CLI 独立配置', 'Pi', 'Agent 配置'], 'pi-registry': ['CLI 独立配置', 'Pi', '模型提供商'], 'pi-auth': ['CLI 独立配置', 'Pi', '认证登录'],
    'omp-agents': ['CLI 独立配置', 'OMP', 'Agent 配置'], 'omp-registry': ['CLI 独立配置', 'OMP', '模型提供商'],
    'opencode-presets': ['CLI 独立配置', 'OpenCode', '预设管理'], 'opencode-global': ['CLI 独立配置', 'OpenCode', '全局配置'],
  };
  const shown = e => !!e && e.offsetParent !== null;
  const rect = e => e.getBoundingClientRect();
  const isSelected = b => b.classList.contains('active') || b.getAttribute('aria-pressed') === 'true';

  // 在 scope 内按文本精确匹配启用的按钮；onlyIfNeeded=true 时已选中则不点（"确保选中"语义，避免对已选页签重复触发 setter）。
  // 返回是否真的点击了。超时抛错，不吞。
  async function pick(scope, text, onlyIfNeeded) {
    for (let i = 0; i < 100; i++) {
      const b = [...document.querySelectorAll(scope + ' button')].find(x => x.textContent.trim() === text && !x.disabled);
      if (b) { if (onlyIfNeeded && isSelected(b)) return false; b.click(); await sleep(80); return true; }
      await sleep(50);
    }
    throw new Error('Button unavailable: ' + scope + ' / ' + text);
  }

  window.__ab = {
    // 显式点击（不判断是否已选中）
    clickText: (scope, text) => pick(scope, text, false),
    // 一级 → 二级 → （三级），逐级"确保选中"
    async seq(view) {
      const p = VIEW[view];
      if (!p) throw new Error('Unknown view: ' + view);
      for (let i = 0; i < 100 && !document.querySelector('.pc-main-tabs'); i++) await sleep(50);
      await pick('.pc-main-tabs', p[0], true);
      await pick('.pc-subnav-tabs', p[1], true);
      if (p[2]) await pick('.pc-subnav-modes', p[2], true);
      return p.join(' > ');
    },
    // 顶侧 subnav：切 CLI 再切该 CLI 的 pill
    async mode(cli, label) {
      await pick('.pc-subnav-tabs', cli, true);
      await pick('.pc-subnav-modes', label, true);
    },
    // 编辑器内部 local 分区导航：按局部 id（data-nav-id）点击
    async section(id) {
      for (let i = 0; i < 100; i++) {
        const nav = document.querySelector(LOCAL_NAV);
        const b = nav && [...nav.querySelectorAll('button[data-nav-id]')].find(x => x.dataset.navId === id);
        if (b && !b.disabled) { b.click(); await sleep(80); return; }
        await sleep(50);
      }
      throw new Error('Local section unavailable: ' + id);
    },
    localNavButtons: () => [...document.querySelectorAll(LOCAL_NAV + ' button[data-nav-id]')],
    localNavIds: () => [...document.querySelectorAll(LOCAL_NAV + ' button[data-nav-id]')].map(b => b.dataset.navId),
    overflow() {
      return [...document.querySelectorAll('.main, .view-provider, .pc-card, .pc-panel, .pc-subnav, .pc-detail-content, .editor-local-layout, .editor-detail, .editor-toolbar, .editor-section, .preset-group, .oc-group')]
        .filter(e => e.offsetParent !== null && e.scrollWidth > e.clientWidth + 1).map(e => ({ cls: e.className, sw: e.scrollWidth, cw: e.clientWidth }));
    },
    // S13 / X11：顶侧页签文案与选中态（只读 DOM）
    tabLabels() {
      const txt = sel => [...document.querySelectorAll(sel + ' button')].map(b => b.textContent.trim());
      const sel = sel => [...document.querySelectorAll(sel + ' button')].find(isSelected)?.textContent.trim() ?? null;
      return {
        main: txt('.pc-main-tabs'), tabs: txt('.pc-subnav-tabs'), modes: txt('.pc-subnav-modes'),
        caption: document.querySelector('.pc-subnav-caption')?.textContent.trim() ?? null,
        activeMain: sel('.pc-main-tabs'), activeTab: sel('.pc-subnav-tabs'), activeMode: sel('.pc-subnav-modes'),
      };
    },
    metrics() {
      const q = s => document.querySelector(s);
      const layout = q('.editor-local-layout');
      const sub = q('.pc-subnav'), tabs = q('.pc-subnav-tabs'), head = q('.pc-head');
      const tb = q('.pc-detail-content .editor-toolbar, .pc-detail-content .pc-toolbar');
      // subnavRows：第二个子元素的 top ≥ 页签组的 bottom 记 2（align-items:flex-end，同一行时第二个元素 top 落在页签组纵向范围内）
      const second = sub ? [...sub.children].filter(c => shown(c) && c !== tabs)[0] : null;
      const subnavRows = !sub || !tabs || !second ? 1 : (rect(second).top >= rect(tabs).bottom - 0.5 ? 2 : 1);
      // toolbarRows：可见叶子按"纵向中心"聚类（±4px）。偏离任务书的"top 取整"：不同高度的叶子（路径胶囊 26px / 状态字 15px / 分段控件 ~28px）
      // 同行 align-items:center 时中心重合而 top 不重合（相差可达 5–7px），按 top 会把单行误判成两行。
      let toolbarRows = null;
      if (shown(tb)) {
        const centers = [...tb.querySelectorAll('.et-path,.et-valid,.et-effect,.segmented,.app-button,.chip,input')].filter(shown)
          .map(e => { const r = rect(e); return Math.round(r.top + r.height / 2); }).sort((a, b) => a - b);
        let rows = 0, anchor = -Infinity;
        for (const c of centers) if (c - anchor > 4) { rows++; anchor = c; }
        toolbarRows = rows;
      }
      const first = [...document.querySelectorAll('.pc-detail-content .editor-section h3, .pc-detail-content .preset-group, .pc-detail-content .oc-group, .pc-detail-content .empty-state')].find(shown);
      const bands = [['pc-head', head], ['pc-subnav', sub], ['toolbar', tb]].filter(([, e]) => shown(e)).map(([n]) => n);
      const activeTab = [...document.querySelectorAll('.pc-subnav-tabs button')].find(isSelected);
      const modes = q('.pc-subnav-modes');
      // 纵向分层（X1 取证）：首内容之上每一层的实高，以及 .pc-head 是否把动作区折到第二行（HEAD 既有行为）
      const pageHead = q('.page-head'), mainTabs = q('.pc-main-tabs'), headActions = q('.pc-head-actions');
      // 与 subnavRows 同口径：动作区的 top ≥ 一级 pill 的 bottom 才算折行（不能比 top 差——同行 align-items:center 时不同高度的两者 top 本就相差约 4px）
      const pcHeadRows = shown(mainTabs) && shown(headActions) ? (rect(headActions).top >= rect(mainTabs).bottom - 0.5 ? 2 : 1) : 1;
      return {
        overflow: this.overflow(),
        side: q('.sidebar')?.getBoundingClientRect().width,
        grid: layout ? getComputedStyle(layout).gridTemplateColumns : null,
        navs: document.querySelectorAll('.view-provider .section-nav').length,
        navsInEditor: [...document.querySelectorAll('.view-provider .section-nav')].filter(n => n.closest(EDITOR_ROOTS)).length,
        pageNav: document.querySelectorAll('.pc-workspace').length,
        subnavCount: document.querySelectorAll('.pc-subnav').length,
        subnavRows, toolbarRows, bands,
        firstContentTop: first ? Math.round(rect(first).top) : null,
        toolbarCls: tb ? tb.className.split(' ')[0] : null,
        toolbarW: tb ? Math.round(rect(tb).width) : null,
        detailW: q('.pc-detail-content') ? Math.round(rect(q('.pc-detail-content')).width) : null,
        pageHeadH: shown(pageHead) ? +rect(pageHead).height.toFixed(1) : null,
        pcHeadH: head ? +rect(head).height.toFixed(1) : null, pcHeadRows,
        toolbarH: tb ? +rect(tb).height.toFixed(1) : null,
        subnavH: sub ? Math.round(rect(sub).height) : null,
        subnavW: sub ? Math.round(rect(sub).width) : null,
        tabsW: tabs ? Math.round(rect(tabs).width) : null,
        modesW: modes ? Math.round(rect(modes).width) : null,
        // 选中 underline 的下边缘与 subnav 通栏分隔线下边缘的距离（0 = 压在分隔线上）
        underlineDelta: activeTab && sub ? +(rect(sub).bottom - rect(activeTab).bottom).toFixed(2) : null,
        sections: [...document.querySelectorAll('.editor-section')].filter(shown).map(e => e.dataset.section),
      };
    },
  };
})();
