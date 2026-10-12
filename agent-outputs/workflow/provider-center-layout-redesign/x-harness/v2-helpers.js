// Browser-only helpers: navigate via real buttons, measure visible DOM; no Vue state mutation.
(() => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  window.__ab = {
    async clickId(id) {
      for (let i = 0; i < 100; i++) {
        const b = [...document.querySelectorAll('button[data-nav-id]')].find(b => b.dataset.navId === id);
        if (b && !b.disabled) { b.click(); await sleep(80); return; }
        await sleep(50);
      }
      throw new Error('Navigation unavailable: ' + id);
    },
    async seq(view) {
      for (let i = 0; i < 100 && !document.querySelector('.pc-main-tabs'); i++) await sleep(50);
      const main = view.startsWith('presets-') ? '预设' : 'CLI 独立配置';
      [...document.querySelectorAll('.pc-main-tabs button')].find(b => b.textContent.trim() === main).click();
      await sleep(80);
      const id = view.startsWith('presets-') ? 'format:' + view.slice(8) : view.replace('-', ':');
      await this.clickId(id);
      return id;
    },
    async section(id) {
      const current = document.querySelector('.section-nav .branch')?.dataset.navId;
      await this.clickId(`section:${current}:${encodeURIComponent(id)}`);
    },
    overflow() {
      return [...document.querySelectorAll('.main, .view-provider, .pc-card, .pc-workspace, .pc-detail-content, .editor-detail, .editor-toolbar, .editor-section, .preset-group, .oc-group')]
        .filter(e => e.offsetParent !== null && e.scrollWidth > e.clientWidth + 1).map(e => ({ cls:e.className, sw:e.scrollWidth, cw:e.clientWidth }));
    },
    metrics() {
      const workspace = document.querySelector('.pc-workspace');
      return { overflow:this.overflow(), grid:workspace && getComputedStyle(workspace).gridTemplateColumns,
        side:document.querySelector('.sidebar')?.getBoundingClientRect().width,
        navs:document.querySelectorAll('.view-provider .section-nav').length,
        sections:[...document.querySelectorAll('.editor-section')].filter(e => e.offsetParent !== null).map(e => e.dataset.section) };
    },
  };
})();
