// Read-only invariant audit against the retained v1 patch (not a restore operation).
import fs from 'node:fs';
import cp from 'node:child_process';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire(process.cwd() + '/frontend/package.json');
const ts = require('typescript');
const base = 'agent-outputs/workflow/provider-center-layout-redesign/';
const git = (...args) => cp.execFileSync('git', args, { encoding:'utf8' });
const patch = fs.readFileSync(base + 'v1-snapshot/tracked.patch', 'utf8');
function v1(file) {
  const head = git('show', 'HEAD:' + file);
  const block = patch.split('diff --git ').find(s => s.startsWith(`a/${file} b/${file}\n`));
  if (!block) return head;
  const original = head.split('\n'), output = []; let cursor = 0;
  const lines = block.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const match = lines[i].match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/);
    if (!match) continue;
    const start = Number(match[1]) - 1;
    output.push(...original.slice(cursor, start)); cursor = start;
    let oldCount = Number(match[2] ?? 1), newCount = Number(match[4] ?? 1);
    while (oldCount || newCount) {
      const line = lines[++i];
      if (line.startsWith('\\')) continue;
      const prefix = line[0], text = line.slice(1);
      if (prefix === ' ' || prefix === '-') { assert.equal(original[cursor++], text); oldCount--; }
      if (prefix === ' ' || prefix === '+') { output.push(text); newCount--; }
    }
  }
  output.push(...original.slice(cursor)); return output.join('\n');
}
function script(text) { return text.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)?.[1] || ''; }
function fn(text, name) {
  const source = ts.createSourceFile('audit.ts', script(text), ts.ScriptTarget.Latest, true);
  const node = source.statements.find(n => ts.isFunctionDeclaration(n) && n.name?.text === name);
  assert(node, name + ' exists'); return node.getText(source);
}
const editors = ['PiAmagiConfig','OmpGlobalConfig','ProviderRegistryEditor','AuthConfigEditor','OpenCodeGlobalConfig'];
for (const name of editors) {
  const file = `frontend/src/components/provider/${name}.vue`, current = fs.readFileSync(file,'utf8'), before = v1(file);
  assert.equal(fn(current,'handleSave'), fn(before,'handleSave'));
  assert(!current.includes('ConfigCategoryCard'));
  assert(!/<h2\b/.test(current));
  assert(current.includes('v-show="activeSection'));
  console.log('PASS S1/S6/S9/S10 ' + name + ': handleSave exact v1 match; no h2/card; visited/v-show present');
}
const viewFile = 'frontend/src/views/ProviderCenterView.vue';
const view = fs.readFileSync(viewFile,'utf8'), oldView = v1(viewFile);
assert.equal(fn(view,'lazyPreset'), fn(oldView,'lazyPreset'));
for (const name of ['onProfileApplied','handleExport','handleImport']) assert.equal(fn(view,name),fn(oldView,name));
assert.equal((view.match(/:key="presetLoadKey"/g) || []).length, 7);
assert(view.includes('snapshot.owner !== navOwner.value'));
assert(!view.includes('pc-subnav'));
assert(view.includes('watch(mainTab, (tab) => {'));
console.log('PASS S3/S6/S11 parent lazy loader/actions exact v1 match; 7 remount keys; owner guard; no subnav');
const allSource = editors.map(n => fs.readFileSync(`frontend/src/components/provider/${n}.vue`,'utf8')).join('\n');
assert(!/<p[^>]*class="(?:pac-hint|pac-subhint|ogc-hint|ogc-subhint)/.test(allSource));
const toolbarUsers = [...editors, 'OpenCodePresets'].filter(n => fs.readFileSync(`frontend/src/components/provider/${n}.vue`,'utf8').includes('<EditorToolbar'));
assert.equal(toolbarUsers.length,6);
console.log('PASS S2 six toolbar users; removed teaching paragraph selectors');
const changed = git('diff','HEAD','--name-only').trim().split('\n');
const prohibited = changed.filter(f => /^(?:frontend\/wailsjs\/|frontend\/src\/(?:api|stores)\/)|\.go$|(?:package(?:-lock)?\.json|yarn\.lock|pnpm-lock\.yaml)$/.test(f));
assert.deepEqual(prohibited, []);
assert.deepEqual(changed.filter(f => f.startsWith('e2e/')).sort(), ['e2e/pi-amagi-config.html','e2e/pi-amagi-config.spec.ts']);
assert.deepEqual(changed.filter(f => f.startsWith('frontend/src/views/')), [viewFile]);
console.log('PASS S7/S8 prohibited source/dependency diffs=0; exactly 2 approved e2e files');
for (const file of ['frontend/src/components/ui/EmptyState.vue','frontend/src/components/ui/StatusBanner.vue','frontend/src/styles/tokens.css','frontend/src/components/provider/ConfigCategoryCard.vue']) assert.equal(fs.readFileSync(file,'utf8'),v1(file));
console.log('PASS v1-only shared components/tokens untouched by v2');
console.log('NOTE skin baseline: App.vue and skinBake.ts are not in git diff; runtime RangeError logged separately (not hidden).');
console.log('Read-only invariant audit complete.');
