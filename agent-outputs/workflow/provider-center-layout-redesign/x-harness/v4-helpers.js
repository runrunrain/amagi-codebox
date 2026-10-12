// v4 浏览器助手：只点击真实按钮、只读 DOM。Wails 隔离 transport 不涉及用户配置。
(() => {
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  const LOCAL_NAV = 'nav.section-nav[aria-label="配置分区"]';
  const ROOTS = '.pac-root,.ogc-root,.pre-root,.ace-root,.oc-global';
  const VIEW = {
    'presets-anthropic':['预设','Anthropic 格式'], 'presets-openai':['预设','OpenAI 格式'],
    'pi-agents':['CLI 独立配置','Pi','Agent 配置'], 'pi-registry':['CLI 独立配置','Pi','模型提供商'], 'pi-auth':['CLI 独立配置','Pi','认证登录'],
    'omp-agents':['CLI 独立配置','OMP','Agent 配置'], 'omp-registry':['CLI 独立配置','OMP','模型提供商'],
    'opencode-presets':['CLI 独立配置','OpenCode','预设管理'], 'opencode-global':['CLI 独立配置','OpenCode','全局配置'],
  };
  const shown = e => !!e && e.getClientRects().length > 0;
  const rect = e => e.getBoundingClientRect();
  const selected = b => b.classList.contains('active') || b.getAttribute('aria-pressed') === 'true';
  const rows = es => {
    const centers = es.filter(shown).map(e => { const r=rect(e); return r.top+r.height/2; }).sort((a,b)=>a-b);
    let count=0, anchor=-Infinity;
    for (const c of centers) if(c-anchor>4){count++;anchor=c;}
    return count;
  };
  async function pick(scope,text,needed) {
    for(let i=0;i<100;i++){
      const b=[...document.querySelectorAll(scope+' button')].find(x=>(x.getAttribute('aria-label')||x.textContent.trim())===text&&!x.disabled);
      if(b){if(needed&&selected(b))return false;b.click();await sleep(80);return true;}await sleep(50);
    }
    throw new Error('Button unavailable: '+scope+' / '+text);
  }
  const OVERFLOW='.main,.view-provider,.pc-card,.pc-panel,.pc-subnav,.pc-subnav-context,.pc-detail-content,.editor-local-layout,.editor-detail,.editor-toolbar,.editor-section,.preset-group,.oc-group,.config-content-group,.ccg-head,.ccg-heading,.ccg-body,.pre-model-head,.provider-editor .pe-thumb-head,.provider-editor .pe-expanded,.agent-editor .ae-thumb-head,.agent-editor .ae-expanded,.mcp-editor .me-thumb-head,.mcp-editor .me-expanded';
  window.__ab={
    clickText:(scope,text)=>pick(scope,text,false),
    async seq(view){const p=VIEW[view];if(!p)throw new Error(view);for(let i=0;i<100&&!document.querySelector('.pc-main-tabs');i++)await sleep(50);await pick('.pc-main-tabs',p[0],true);await pick('.pc-subnav-tabs',p[1],true);if(p[2])await pick('.pc-subnav-context .pc-subnav-modes',p[2],true);return p.join(' > ');},
    async mode(cli,label){await pick('.pc-subnav-tabs',cli,true);await pick('.pc-subnav-context .pc-subnav-modes',label,true);},
    async section(id){for(let i=0;i<100;i++){const b=[...document.querySelectorAll(LOCAL_NAV+' button[data-nav-id]')].find(x=>x.dataset.navId===id);if(b&&!b.disabled){b.click();await sleep(80);return;}await sleep(50);}throw new Error('Local section unavailable: '+id);},
    localNavButtons:()=>[...document.querySelectorAll(LOCAL_NAV+' button[data-nav-id]')],
    localNavIds:()=>[...document.querySelectorAll(LOCAL_NAV+' button[data-nav-id]')].map(b=>b.dataset.navId),
    overflow(){
      const result=[...document.querySelectorAll(OVERFLOW)].filter(e=>shown(e)&&e.scrollWidth>e.clientWidth+1).map(e=>({cls:e.className,sw:e.scrollWidth,cw:e.clientWidth}));
      // 内层外接框：不能用外壳 scrollWidth 掩盖控件横向越界；只测新增归组/展开区，过滤不可见祖先。
      for(const e of document.querySelectorAll('.config-content-group input,.config-content-group button,.oc-global .pe-expanded input,.oc-global .ae-expanded input,.oc-global .me-expanded input,.oc-global .pe-expanded button,.oc-global .ae-expanded button,.oc-global .me-expanded button')){
        if(!shown(e))continue;const host=e.closest('.ccg-body,.ccg-heading,.pe-expanded,.ae-expanded,.me-expanded');if(!shown(host))continue;const a=rect(e),b=rect(host);if(a.left<b.left-1||a.right>b.right+1)result.push({cls:e.className,left:a.left,right:a.right,host:host.className});
      }
      return result;
    },
    navPurity(){return [...document.querySelectorAll(LOCAL_NAV+' button[data-nav-id]')].map(b=>{
      const d=document.getElementById(b.getAttribute('aria-describedby'));const cs=d&&getComputedStyle(d);
      return {id:b.dataset.navId,name:b.getAttribute('aria-label'),visibleChildren:[...b.children].filter(e=>!e.classList.contains('sn-description')).map(e=>e.className),description:d?.textContent||'',css:cs?{position:cs.position,width:cs.width,height:cs.height,clip:cs.clip,clipPath:cs.clipPath,display:cs.display,visibility:cs.visibility}:null};
    });},
    tabLabels(){const txt=s=>[...document.querySelectorAll(s+' button')].map(b=>b.textContent.trim());const sel=s=>[...document.querySelectorAll(s+' button')].find(selected)?.textContent.trim()??null;return {main:txt('.pc-main-tabs'),tabs:txt('.pc-subnav-tabs'),modes:txt('.pc-subnav-modes'),caption:document.querySelector('.pc-subnav-caption')?.textContent.trim()??null,activeMain:sel('.pc-main-tabs'),activeTab:sel('.pc-subnav-tabs'),activeMode:sel('.pc-subnav-modes')};},
    metrics(){
      const q=s=>document.querySelector(s),layout=q('.editor-local-layout'),sub=q('.pc-subnav'),tabs=q('.pc-subnav-tabs'),context=q('.pc-subnav-context'),owner=q('.pc-subnav-owner'),head=q('.pc-head'),tb=q('.pc-detail-content .editor-toolbar,.pc-detail-content .pc-toolbar'),modes=q('.pc-subnav-modes');
      const contextRows=context?rows([...context.children]):0;
      const caption=q('.pc-subnav-caption');
      const subnavRows=context?1+contextRows:(shown(caption)&&shown(tabs)&&rect(caption).top>=rect(tabs).bottom-.5?2:1);
      const first=[...document.querySelectorAll('.pc-detail-content .editor-section h3,.pc-detail-content .preset-group,.pc-detail-content .oc-group,.pc-detail-content .empty-state')].find(shown);
      const editable=[...document.querySelectorAll('.editor-section input,.editor-section textarea,.editor-section .dropdown-trigger,.editor-section .segmented button,.editor-section .pe-thumb-head,.editor-section .ae-thumb-head,.editor-section .me-thumb-head,.preset-open,.oc-open')].find(shown);
      const activeTab=[...document.querySelectorAll('.pc-subnav-tabs button')].find(selected),pageHead=q('.page-head'),mainTabs=q('.pc-main-tabs'),actions=q('.pc-head-actions');
      return {
        overflow:this.overflow(),side:q('.sidebar')?.getBoundingClientRect().width,grid:layout?getComputedStyle(layout).gridTemplateColumns:null,
        navs:document.querySelectorAll('.view-provider .section-nav').length,navsInEditor:[...document.querySelectorAll('.view-provider .section-nav')].filter(n=>n.closest(ROOTS)).length,pageNav:document.querySelectorAll('.pc-workspace').length,subnavCount:document.querySelectorAll('.pc-subnav').length,
        subnavRows,contextRows,contextOwner:owner?.textContent.trim()??null,contextIndent:context?+(rect(context).left-rect(sub).left).toFixed(2):null,
        toolbarRows:shown(tb)?rows([...tb.querySelectorAll('.et-path,.et-valid,.et-effect,.segmented,.app-button,.chip,input')]):null,bands:[['pc-head',head],['pc-subnav',sub],['toolbar',tb]].filter(([,e])=>shown(e)).map(([n])=>n),
        firstContentTop:first?Math.round(rect(first).top):null,firstEditableTop:editable?Math.round(rect(editable).top):null,
        toolbarCls:tb?tb.className.split(' ')[0]:null,toolbarW:tb?Math.round(rect(tb).width):null,detailW:q('.pc-detail-content')?Math.round(rect(q('.pc-detail-content')).width):null,
        pageHeadH:shown(pageHead)?+rect(pageHead).height.toFixed(1):null,pcHeadH:head?+rect(head).height.toFixed(1):null,pcHeadRows:shown(mainTabs)&&shown(actions)?(rect(actions).top>=rect(mainTabs).bottom-.5?2:1):1,
        toolbarH:tb?+rect(tb).height.toFixed(1):null,subnavH:sub?Math.round(rect(sub).height):null,subnavW:sub?Math.round(rect(sub).width):null,tabsW:tabs?Math.round(rect(tabs).width):null,modesW:modes?Math.round(rect(modes).width):null,
        underlineDelta:activeTab&&sub?+((context?rect(tabs):rect(sub)).bottom-rect(activeTab).bottom).toFixed(2):null,
        sections:[...document.querySelectorAll('.editor-section')].filter(shown).map(e=>e.dataset.section),
      };
    },
  };
})();
