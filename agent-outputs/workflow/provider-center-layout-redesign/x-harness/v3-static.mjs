// v3 只读不变量审计。基线 = `git show HEAD:<file>`（HEAD 的代码树与 v1.3.96 发布态逐字节一致，开头先校验）。
// 不 restore / reset / checkout，不写共享树；只读 git 与文件。与 v2-static.mjs 的区别：不再用「HEAD + v1 patch」重建基线。
import fs from 'node:fs';
import cp from 'node:child_process';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(process.cwd() + '/frontend/package.json');
const ts = require('typescript');
const gitRaw = (...args) => cp.spawnSync('git', args, { encoding: 'utf8' });
const git = (...args) => { const r = gitRaw(...args); assert.equal(r.status, 0, `git ${args.join(' ')} 失败: ${r.stderr}`); return r.stdout; };
const lines = s => s.split('\n').filter(Boolean);

// ---- 0 基线 ----
const headSha = git('rev-parse', 'HEAD').trim();
const describe = git('describe', '--tags').trim();
assert.equal(gitRaw('diff', '--quiet', 'v1.3.96', 'HEAD', '--', 'frontend', 'e2e').status, 0, 'frontend/ e2e/ 应与 v1.3.96 发布提交逐字节一致');
console.log(`PASS baseline: HEAD=${headSha} (${describe})；frontend/ 与 e2e/ 相对 tag v1.3.96 无差异（HEAD 比 tag 多的提交只改 agent-outputs 文档）`);

// ---- ProviderCenterView：<script setup> 逐语句比对 ----
const viewFile = 'frontend/src/views/ProviderCenterView.vue';
const view = fs.readFileSync(viewFile, 'utf8');
const head = git('show', 'HEAD:' + viewFile);
const scriptOf = t => t.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)?.[1] || '';
function parse(text) {
  const sf = ts.createSourceFile('audit.ts', scriptOf(text), ts.ScriptTarget.Latest, true);
  const map = new Map();
  for (const n of sf.statements) {
    let key;
    if (ts.isImportDeclaration(n)) key = 'import:' + n.moduleSpecifier.getText(sf);
    else if (ts.isFunctionDeclaration(n)) key = 'function:' + n.name.text;
    else if (ts.isVariableStatement(n)) key = 'var:' + n.declarationList.declarations.map(d => d.name.getText(sf)).join(',');
    else if (ts.isExpressionStatement(n) && ts.isCallExpression(n.expression)) {
      const callee = n.expression.expression.getText(sf), a0 = n.expression.arguments[0];
      key = `call:${callee}(${callee === 'watch' && a0 ? a0.getText(sf).replace(/\s+/g, ' ') : ''})`;
    } else key = 'other:' + n.getText(sf).slice(0, 40);
    assert(!map.has(key), '重复语句键 ' + key);
    map.set(key, n.getText(sf));
  }
  return { sf, map };
}
const H = parse(head), C = parse(view);
const removed = [...H.map.keys()].filter(k => !C.map.has(k)).sort();
const added = [...C.map.keys()].filter(k => !H.map.has(k)).sort();
const changed = [...C.map.keys()].filter(k => H.map.has(k) && H.map.get(k) !== C.map.get(k)).sort();
const EXPECT_REMOVED = [
  "import:'../components/ui/SectionNav.vue'",
  'var:sectionMemory', 'var:navSnapshot', 'var:navOwner', 'var:mountSequence',
  'call:watch([activePageId, presetLoadKey])',
  'var:desiredSection', 'var:editorNavigation', 'function:onNavigationChange',
  'var:navGroups', 'var:navigationTargets', 'var:currentNavId', 'var:navigationNotice', 'function:selectNavigation',
].sort();
const EXPECT_ADDED = ['var:FORMAT_TABS', 'var:CLI_TABS', 'var:modeTabs', 'var:PI_MODES', 'var:OMP_MODES', 'var:OPENCODE_MODES'].sort();
const EXPECT_CHANGED = ["import:'../components/provider/configNavigation'", 'var:PresetLoadError'].sort();
assert.deepEqual(removed, EXPECT_REMOVED, '删除的顶层语句必须恰好等于 §3.1「删除」清单');
assert.deepEqual(added, EXPECT_ADDED, '新增的顶层语句必须恰好等于 §3.1「页签常量」');
assert.deepEqual(changed, EXPECT_CHANGED, '改动的顶层语句必须恰好是 configNavigation 的 import 与 PresetLoadError');
const retained = [...C.map.keys()].filter(k => H.map.has(k) && !changed.includes(k));
console.log(`PASS S6 <script setup> 逐语句比对：删除 ${removed.length} 条（=§3.1 清单）、新增 ${added.length} 条（=页签常量）、改动 2 条（import、PresetLoadError）、其余 ${retained.length} 条与 HEAD 逐字节相同`);

// 保留清单逐项点名（逐字不动）
for (const k of ['function:lazyPreset', 'function:onProfileApplied', 'function:handleExport', 'function:handleImport', 'var:formatModel', 'var:cliModel', 'var:formatConsumers', 'var:presetLoadKey', 'var:isWorkspace', 'var:activePageId',
  'var:OpenCodePresets', 'var:OpenCodeGlobalConfig', 'var:PiAmagiConfig', 'var:OmpGlobalConfig', 'var:ProviderRegistryEditor', 'var:AuthConfigEditor', 'call:watch(mainTab)', 'call:watch(() => store.activeProviderId)']) {
  assert(C.map.has(k), '缺少保留语句 ' + k);
  assert.equal(C.map.get(k), H.map.get(k), k + ' 应与 HEAD 逐字相同');
}
console.log('PASS S6 保留清单点名：lazyPreset／onProfileApplied／handleExport／handleImport／formatModel／cliModel（setter）／formatConsumers／presetLoadKey／isWorkspace／activePageId／六个 lazyPreset 声明／watch(mainTab)／watch(activeProviderId) 与 HEAD 逐字相同');

// PresetLoadError：onRetry 箭头函数（整段，含整页重载防循环逻辑）与 HEAD 逐字相同；setup 已缩减为只返回渲染函数
function onRetry(parsed) {
  let found = null;
  const visit = n => { if (ts.isPropertyAssignment(n) && n.name.getText(parsed.sf) === 'onRetry') found = n.initializer.getText(parsed.sf); ts.forEachChild(n, visit); };
  const decl = parsed.sf.statements.find(n => ts.isVariableStatement(n) && n.declarationList.declarations[0].name.getText(parsed.sf) === 'PresetLoadError');
  visit(decl); assert(found, '找不到 onRetry'); return found;
}
assert.equal(onRetry(C), onRetry(H));
assert(onRetry(C).includes("window.sessionStorage.getItem(CHUNK_RELOAD_FLAG)") && onRetry(C).includes('window.location.reload()') && onRetry(C).includes('presetLoadKey.value++'));
const ple = C.map.get('var:PresetLoadError');
for (const gone of ['onMounted', 'navOwner', 'navSnapshot', 'owner', 'staticSections']) assert(!ple.includes(gone), `PresetLoadError 不应再含 ${gone}`);
console.log('PASS S6 PresetLoadError.onRetry 整段（含 sessionStorage 整页重载防循环）与 HEAD 逐字相同；setup 已缩减（无 owner／onMounted／snapshot 上报）');

// ---- 模板：被替换的面板块之外，与 HEAD 逐字节相同（忽略顶部说明注释）----
const tplOf = t => t.slice(t.indexOf('<template>'), t.indexOf('</template>\n\n<script'));
const stripHeadComment = s => s.replace(/<!--[\s\S]*?-->/, '');   // 仅第一段说明注释
function outside(t, marker) {
  const tpl = tplOf(t), a = tpl.indexOf(marker), b = tpl.indexOf('    </ConfigCard>');
  assert(a > 0 && b > a, '定位面板块失败 ' + marker);
  return stripHeadComment(tpl.slice(0, a)) + tpl.slice(b);
}
assert.equal(outside(view, '<div v-else class="pc-panel">'), outside(head, '<div v-else class="pc-workspace">'));
console.log('PASS 模板：被替换的 else 面板块之外（PageHead／远程横幅／详情视图／一级 pill 与 pc-head 动作／服务提供商面板／对话框）与 HEAD 逐字节相同');

// ---- S3 ----
for (const bad of ['<SectionNav', 'pc-workspace"', 'navOwner', 'navigation-change', 'editorNavigation', 'pc-editor-detail', 'pc-format-caption']) assert(!view.includes(bad), `视图不应含 ${bad}`);
assert.equal((view.match(/class="pc-subnav"/g) || []).length, 1, '模板里 .pc-subnav 恰好 1 处（一处 v-if/else 承载两页）');
assert(!/\bnavigation=|:navigation\b|section-id|nav-owner/.test(view), '五个编辑器应走默认 navigation=local，不传 navigation/section-id/nav-owner');
assert.equal((view.match(/:key="presetLoadKey"/g) || []).length, 7);
assert.equal((head.match(/:key="presetLoadKey"/g) || []).length, 7);
assert.equal((view.match(/:key="activePageId"/g) || []).length, 1);
assert(view.includes('watch(mainTab, (tab) => {'));
console.log('PASS S3 视图不含 <SectionNav／pc-workspace"／navOwner／navigation-change；.pc-subnav=1；:key="presetLoadKey"=7（HEAD 亦 7）；外层 :key="activePageId"=1；watch(mainTab) 在');

// ---- S13（静态半边）：页签选项只派生自 CONFIG_PAGES／FORMAT_ITEMS，视图里不复制文案 ----
// 只扫描「代码里的字符串字面量」与「去注释后的模板」——HEAD 保留的 formatConsumers 上方注释里本来就写着「Anthropic 格式／OpenAI 格式」，注释不算文案复制。
const literals = [];
(function walk(n) { if (ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n) || ts.isTemplateHead(n) || ts.isTemplateMiddle(n) || ts.isTemplateTail(n)) literals.push(n.text); ts.forEachChild(n, walk); })(C.sf);
const tplText = tplOf(view).replace(/<!--[\s\S]*?-->/g, '');
for (const label of ['Anthropic 格式', 'OpenAI 格式', 'Agent 配置', '模型提供商', '认证登录', '预设管理', '全局配置']) {
  assert(!literals.some(s => s.includes(label)), `视图脚本不应硬编码页签文案「${label}」`);
  assert(!tplText.includes(label), `视图模板不应硬编码页签文案「${label}」`);
}
const consts = ['FORMAT_TABS', 'CLI_TABS', 'modeTabs'].map(k => C.map.get('var:' + k)).join('\n');
assert(consts.includes('FORMAT_ITEMS') && consts.includes('CONFIG_PAGES'));
console.log('PASS S13(静态) 页签选项只引用 FORMAT_ITEMS／CONFIG_PAGES，视图内无页签文案硬编码（渲染文本与 v1 逐一相等由 v3-runtime coverage 读 DOM 断言）');

// ---- 样式 ----
const styleOf = t => t.match(/<style scoped>([\s\S]*?)<\/style>/)[1];
const css = styleOf(view), cssHead = styleOf(head);
for (const gone of ['.pc-workspace {', '.pc-format-caption', '--section-nav-width']) assert(!css.includes(gone), `样式不应再含 ${gone}`);
for (const kept of ['.pc-workspace-view', '.pc-card-workspace', '.pc-card-workspace .pc-head-actions { flex-wrap:wrap; min-width:0; max-width:100%; }', '.pc-detail-content { min-width:0; }']) { assert(cssHead.includes(kept) && css.includes(kept), `样式应保留 ${kept}`); }
for (const add of ['.pc-subnav {', '.pc-subnav-tabs.underline-tabs {', '.pc-subnav-caption {', '.pc-subnav-modes {', '.pc-subnav-modes.segmented {', '.pc-subnav-modes :deep(.seg) {']) assert(css.includes(add), `样式应新增 ${add}`);
const narrow = css.slice(css.indexOf('@media (max-width:1023px)'));
assert(narrow.includes('.pc-subnav-tabs.underline-tabs { gap:16px; }') && narrow.includes('.pc-subnav-modes :deep(.seg) { padding-inline:10px; }'));
assert(css.indexOf('.pc-subnav-modes :deep(.seg) {') < css.indexOf('@media (max-width:1023px)'), '窄档覆盖规则必须在基础规则之后');
console.log('PASS 样式：删除 .pc-workspace 网格／.pc-format-caption／--section-nav-width；保留两个窄档边距类、.pc-head-actions 换行规则；新增 v1 subnav 规则＋<1024 窄档收紧');

// ---- S7（收紧）----
const changedFiles = lines(git('diff', 'HEAD', '--name-only'));
const codeChanged = changedFiles.filter(f => !f.startsWith('agent-outputs/'));
assert.deepEqual(codeChanged, [viewFile], 'agent-outputs/ 之外的受跟踪改动只能是 ' + viewFile);
const feE2e = changedFiles.filter(f => f.startsWith('frontend/') || f.startsWith('e2e/'));
assert.deepEqual(feE2e, [viewFile]);
const porcelain = lines(git('status', '--porcelain', '--', 'frontend', 'e2e')).map(l => l.slice(3));
assert.deepEqual(porcelain, [viewFile], '含未跟踪文件在内，frontend/ 与 e2e/ 下也只能有这一个文件');
const prohibited = changedFiles.filter(f => /^(?:frontend\/wailsjs\/|frontend\/src\/(?:api|stores)\/)|\.go$|(?:package(?:-lock)?\.json|yarn\.lock|pnpm-lock\.yaml)$/.test(f));
assert.deepEqual(prohibited, []);
const MUST_BE_ZERO = [
  'e2e', 'frontend/src/components/provider/configNavigation.ts', 'frontend/src/components/ui',
  ...['PiAmagiConfig', 'OmpGlobalConfig', 'ProviderRegistryEditor', 'AuthConfigEditor', 'OpenCodeGlobalConfig'].map(n => `frontend/src/components/provider/${n}.vue`),
  'frontend/src/styles/tokens.css', 'frontend/src/__tests__', 'frontend/wailsjs', 'frontend/src/api', 'frontend/src/stores', 'frontend/package.json', 'frontend/package-lock.json', 'package.json',
];
for (const p of MUST_BE_ZERO) if (fs.existsSync(p)) assert.equal(gitRaw('diff', '--quiet', 'HEAD', '--', p).status, 0, `${p} 必须零改动`);
console.log(`PASS S7 agent-outputs/ 之外的受跟踪改动 = [${codeChanged.join(', ')}]；frontend/ 与 e2e/（含未跟踪）只有该文件；Go／wailsjs／api／stores／manifests／lockfile = 0；五个编辑器／configNavigation／ui/*／tokens／__tests__／e2e 零改动`);

// ---- S11 / external 休眠登记 ----
const editors = ['PiAmagiConfig', 'OmpGlobalConfig', 'ProviderRegistryEditor', 'AuthConfigEditor', 'OpenCodeGlobalConfig'];
const defaultsLocal = editors.filter(n => /withDefaults\(defineProps<[^;]*\{ navigation: 'local'/.test(fs.readFileSync(`frontend/src/components/provider/${n}.vue`, 'utf8')));
assert.deepEqual(defaultsLocal, editors, '五个编辑器 navigation 默认值应为 local');
const nav = fs.readFileSync('frontend/src/components/provider/configNavigation.ts', 'utf8');
assert(nav.includes("navigation?: 'external' | 'local'"), 'external 模式契约按设计休眠保留（C1 清单待主上验收后删除）');
console.log('PASS S11 父级不消费 navigation-change（视图无该事件监听）；snapshot 不离开编辑器；五个编辑器默认 local；external 分支休眠保留（设计 §3.2 选 B，C1 待清理）');
console.log('NOTE S1/S2/S4/S5/S9/S10/S12：五个编辑器与共享 UI 相对 HEAD 零改动，这些条款按 HEAD 状态成立（不重复论证）');
console.log('Read-only invariant audit (v3) complete.');
