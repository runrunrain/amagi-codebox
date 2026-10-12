/*
 * X 系列辅助 init script：在页面里挂 window.__ab，供 agent-browser eval 调用（导航 / 展开 / 度量）。
 * 只读 + 点击已有按钮，不改应用代码与数据。
 */
(() => {
  const norm = (s) => (s || '').replace(/\s+/g, ' ').trim();
  const ab = {
    /** 按精确文本找按钮（可限定作用域选择器） */
    btn(text, scope) {
      const root = scope ? document.querySelector(scope) : document;
      return root ? [...root.querySelectorAll('button')].find((b) => norm(b.textContent) === text) : null;
    },
    click(text, scope) {
      const b = this.btn(text, scope);
      if (!b) return 'NOT FOUND: ' + text;
      b.click();
      return 'clicked: ' + text;
    },
    /** 展开全部折叠的 ConfigCategoryCard */
    expandAll() {
      const hs = [...document.querySelectorAll('.config-cat-card .ccc-header[aria-expanded="false"]')];
      hs.forEach((h) => h.click());
      return 'expanded ' + hs.length;
    },
    /** 视图名 → 点击序列；逐个点击之间等一帧，保证 Vue 已重渲染出下一个按钮（返回 Promise，agent-browser eval 会等待） */
    async seq(view) {
      const S = {
        'presets-anthropic': ['预设'],
        'presets-openai': ['预设', 'OpenAI 格式'],
        'pi-agents': ['CLI 独立配置'],
        'pi-registry': ['CLI 独立配置', '模型提供商'],
        'pi-auth': ['CLI 独立配置', '认证登录'],
        'omp-agents': ['CLI 独立配置', 'OMP'],
        'omp-registry': ['CLI 独立配置', 'OMP', '模型提供商'],
        'opencode-presets': ['CLI 独立配置', 'OpenCode', '预设管理'],
        'opencode-global': ['CLI 独立配置', 'OpenCode'],
      }[view];
      if (!S) return 'unknown view ' + view;
      const out = [];
      const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
      // 轮询等待按钮出现（应用启动 / 懒加载有时序），最多 6s
      const waitBtn = async (t) => {
        for (let i = 0; i < 60; i++) {
          if (this.btn(t, '.view-provider')) return true;
          await sleep(100);
        }
        return false;
      };
      for (const t of S) {
        if (!(await waitBtn(t))) { out.push('NOT FOUND: ' + t); break; }
        out.push(this.click(t, '.view-provider'));
        await sleep(250);
      }
      return out.join(' | ');
    },
    /**
     * 度量（design P2）：首个内容元素之前的 chrome 行数。
     * chrome 块 = .pc-head（一级导航）/ .pc-subnav（二三级合并导航）/ .editor-toolbar | .pc-toolbar（工具栏）/ .status-banner（横幅）。
     * 首个内容元素 = 卡片列表 / 分组列表 / 空态 / 源码编辑器 / 列表之类的第一个非 chrome 区块。
     */
    chrome() {
      const card = document.querySelector('.pc-card');
      if (!card) return 'no card';
      const CH = '.pc-head, .pc-subnav, .editor-toolbar, .pc-toolbar, .status-banner';
      const CONTENT = '.config-cat-card, .preset-group, .oc-group, .ace-entry, .ace-add, .ace-empty, .pre-empty, .empty-state, .pac-json-editor, .ogc-source-editor, .pre-source-editor, .ace-source-editor, .ocg-json-editor, .oc-json';
      const first = card.querySelector(CONTENT);
      const all = [...card.querySelectorAll(CH)].filter((e) => e.offsetParent !== null);
      const before = first ? all.filter((e) => e.compareDocumentPosition(first) & Node.DOCUMENT_POSITION_FOLLOWING) : all;
      const top = card.getBoundingClientRect().top;
      return JSON.stringify({
        chromeRows: before.map((e) => e.className.toString().split(' ')[0]),
        count: before.length,
        pxCardTopToFirstContent: first ? Math.round(first.getBoundingClientRect().top - top) : null,
        first: first ? first.className.toString().split(' ')[0] : null,
      });
    },
    /** 度量：可见的常显说明块（.pac-caption / .sle-empty-text / .oc-desc 等）及字数 */
    captions() {
      const sel = '.pac-caption, .sle-empty-text, .pre-unit, .ocg-note, .pc-subnav-caption';
      return JSON.stringify([...document.querySelectorAll(sel)].filter((e) => e.offsetParent !== null).map((e) => ({ cls: e.className.toString().split(' ')[0], len: norm(e.textContent).length, text: norm(e.textContent) })));
    },
    /** 度量：页面是否出现横向溢出（容器 scrollWidth > clientWidth） */
    overflow() {
      const out = [];
      document.querySelectorAll('.main, .view-provider, .pc-card, .pc-panel, .editor-toolbar, .config-cat-card, .preset-group, .oc-group').forEach((e) => {
        if (e.scrollWidth > e.clientWidth + 1) out.push({ cls: e.className.toString().split(' ').slice(0, 2).join('.'), sw: e.scrollWidth, cw: e.clientWidth });
      });
      return JSON.stringify(out);
    },
  };
  window.__ab = ab;
})();
