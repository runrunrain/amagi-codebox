<template>
  <section class="ai-proxy-page">
    <header class="page-top">
      <PageHead title="AI 代理分流" description="按服务添加域名规则，分别选择代理节点或直连。" />
      <span class="run-status" :class="{ running: state?.status.running, error: state?.enabled && !state?.status.running }">{{ statusText }}</span>
    </header>
    <StatusBanner v-if="error" type="error" :message="error" />
    <StatusBanner v-if="state?.error" type="error" :message="state.error" />
    <p v-if="!state" class="hint">正在读取代理配置…</p>
    <template v-if="state && form">
      <div class="feature-bar">
        <div><strong>独立管理，本机会话生效</strong><p class="hint">Claude Code、Codex、Pi、OMP、OpenCode 共用域名分流规则；其他域名默认直连。</p></div>
        <div class="actions"><AppButton :disabled="busy || !state.status.running" @click="stop">停止</AppButton><AppButton :disabled="busy" @click="save">保存配置</AppButton><AppButton variant="primary" :disabled="busy || !canApply" @click="apply">{{ busy ? '处理中…' : state.status.running ? '应用更改' : '启用分流' }}</AppButton></div>
      </div>
      <p v-if="dirty || state.pending" class="pending">有待应用的更改。应用会重启本功能的代理内核，现有连接将断开。</p>
      <div class="section-title"><div><h2>服务分流规则</h2><p class="hint">按从上到下的顺序匹配，首个命中生效；域名包含其子域名。专用入口始终使用所属规则的出口。</p></div><AppButton :disabled="busy" @click="addRule">添加规则</AppButton></div>
      <p v-if="!form.routing.rules.length" class="empty">尚无服务规则，所有请求使用其他域名的默认策略。</p>
      <div class="route-grid">
        <ConfigCard v-for="(rule, index) in form.routing.rules" :key="rule.id" :class="{ 'rule-disabled': !rule.enabled }">
          <div class="rule-head">
            <span class="rule-number">{{ index + 1 }}</span>
            <input v-model="rule.name" class="input rule-name" :aria-label="`规则 ${index + 1} 名称`" maxlength="80" placeholder="服务名称" :disabled="busy" />
            <label class="rule-toggle"><input v-model="rule.enabled" type="checkbox" :aria-label="`启用规则 ${index + 1}`" :disabled="busy" />启用</label>
          </div>
          <div class="actions rule-actions"><AppButton size="small" :disabled="busy || index === 0" :aria-label="`上移规则 ${index + 1}`" @click="moveRule(index, -1)">上移</AppButton><AppButton size="small" :disabled="busy || index === form.routing.rules.length - 1" :aria-label="`下移规则 ${index + 1}`" @click="moveRule(index, 1)">下移</AppButton><AppButton size="small" :disabled="busy" :aria-label="`移除规则 ${index + 1}`" @click="removeRule(rule.id)">移除</AppButton></div>
          <label :for="`${rule.id}-node`">出口方式与节点</label>
          <select :id="`${rule.id}-node`" :value="rule.mode === 'direct' ? '@direct' : rule.node" class="sel" :disabled="busy" @change="setTarget(rule, $event)">
            <option value="">请选择代理节点</option><option value="@direct">直连</option>
            <optgroup v-for="group in state.groups" :key="group.id" :label="group.name"><option v-for="node in group.nodes" :key="node.id" :value="node.id">{{ node.name }} · {{ node.type.toUpperCase() }}</option></optgroup>
            <option v-if="rule.mode === 'proxy' && missing(rule.node)" :value="rule.node">原节点已失效，请重新选择</option>
          </select>
          <label :for="`${rule.id}-domains`">匹配域名</label>
          <textarea :id="`${rule.id}-domains`" :value="domainText[rule.id] ?? rule.domains.join('\n')" rows="3" spellcheck="false" placeholder="api.example.com，每行一个域名" :disabled="busy" @input="setDomains(rule, $event)" />
          <label :for="`${rule.id}-port`">专用端口（可选）<input :id="`${rule.id}-port`" :value="rule.port || ''" class="input" type="number" min="1024" max="65535" placeholder="留空：仅使用统一入口" :disabled="busy" @input="setPort(rule, $event)" /></label>
          <p v-if="rule.port" class="hint">专用入口：127.0.0.1:{{ rule.port }}</p>
        </ConfigCard>
      </div>
      <p v-if="!canApply" class="hint">请为已启用的规则填写域名或专用端口，并选择有效代理节点或直连。</p>
      <ConfigCard>
        <div class="section-title"><div><h2>v2rayN 与订阅节点</h2><p class="hint">{{ sourceText }}</p></div><AppButton :disabled="busy" @click="sync">同步 v2rayN</AppButton></div>
        <p v-if="!state.groups.length" class="empty">尚无节点。同步本机 v2rayN，或展开下方手动导入。</p>
        <div v-for="group in state.groups" :key="group.id" class="group-row">
          <div><strong>{{ group.name }}</strong><p class="hint">{{ group.source === 'v2rayn' ? '来自 v2rayN · ' : '' }}{{ group.nodes.length }} 个节点</p><details v-if="group.warnings?.length"><summary>{{ group.warnings.length }} 条兼容性提示</summary><p v-for="warning in group.warnings" :key="warning" class="hint">{{ warning }}</p></details></div>
          <div class="actions"><AppButton v-if="group.refreshable" size="small" :disabled="busy" @click="refresh(group.id)">更新</AppButton><AppButton size="small" :disabled="busy" @click="remove(group.id)">移除</AppButton></div>
        </div>
        <details class="import"><summary>手动导入订阅或节点</summary><form @submit.prevent="importNodes"><div class="import-fields"><label>名称<input v-model="importForm.name" class="input" required placeholder="订阅名称" /></label><label>订阅地址<input v-model="importForm.url" class="input" type="url" autocomplete="off" placeholder="https://…" /></label></div><label>或粘贴节点内容<textarea v-model="importForm.content" rows="3" autocomplete="off" placeholder="节点链接、Base64、Clash YAML 或 sing-box JSON" /></label><AppButton variant="primary" :disabled="busy || !importForm.name.trim()" @click="importNodes">导入</AppButton></form></details>
      </ConfigCard>
      <ConfigCard>
        <h2>CodeBox 接入</h2>
        <div class="integration-grid"><div><strong>新建会话自动生效</strong><p class="hint">内嵌、外部会话及宿主远程启动统一接入。Pi / OMP 的多模型与子 Agent 按请求域名分别分流。</p></div><div><strong>国内模型默认直连</strong><p class="hint">GLM 等未匹配域名走直连。第三方 API 可把其域名加入上方规则；本地服务保持回环直连。</p></div><div><strong>压缩与模型探测一致</strong><p class="hint">新启动的 Headroom、提交总结模型、多模态与额度探测使用相同分流。已有会话和 Headroom 需重启后接入。</p></div></div>
        <p class="hint">当前管理的是本机。远程宿主使用宿主自己的配置；WSL 回环转发暂不支持。v2rayN 开启 TUN 时可能形成代理套代理。</p>
      </ConfigCard>
      <ConfigCard><details><summary>高级设置与运行日志</summary><label>统一入口端口<input v-model.number="form.routing.proxy_port" class="input" type="number" min="1024" max="65535" /></label><label>其他域名上游（留空直连）<input v-model="form.routing.fallback_proxy" class="input" autocomplete="off" placeholder="留空：国内模型及其他域名直连" /></label><label>订阅下载代理（留空直连）<input v-model="form.routing.fetch_proxy" class="input" autocomplete="off" placeholder="http://127.0.0.1:5800" /></label><label>sing-box 路径（留空自动查找）<input v-model="form.corePath" class="input" :placeholder="state.status.core || '自动查找 v2rayN 内核或 PATH'" /></label><label>v2rayN 数据库（留空自动查找）<input v-model="form.v2raynDatabase" class="input" :placeholder="state.source.database || 'guiConfigs/guiNDB.db'" /></label><p class="hint">统一入口：127.0.0.1:{{ form.routing.proxy_port }}。所有入口仅监听本机；停止分流后，新会话恢复原有代理策略。</p><pre>{{ state.status.error || state.status.logs || '暂无运行日志' }}</pre></details></ConfigCard>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onActivated, onDeactivated, ref } from 'vue'
import { aiproxy, router as proxyRouter } from '../../wailsjs/go/models'
import * as api from '../api/aiProxy'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import StatusBanner from '../components/ui/StatusBanner.vue'
import { useToast } from '../composables/useToast'

const state = ref<api.AIProxyState | null>(null)
const form = ref<api.AIProxySettings | null>(null)
const domainText = ref<Record<string, string>>({})
const busy = ref(false), error = ref('')
const importForm = ref({ name: '', url: '', content: '' })
const { showSuccess } = useToast()
const dirty = computed(() => state.value && form.value && JSON.stringify(state.value.settings) !== JSON.stringify(form.value))
const ids = computed(() => new Set(state.value?.groups.flatMap(g => g.nodes.map(n => n.id)) || []))
const missing = (id: string) => !!id && !ids.value.has(id)
const canApply = computed(() => form.value && form.value.routing.rules.every(rule => !rule.enabled || (rule.name.trim() && (rule.domains.length || rule.port) && (rule.mode === 'direct' || ids.value.has(rule.node)))))
const statusText = computed(() => state.value?.status.running ? (state.value.pending ? '运行中 · 待应用' : '分流运行中') : state.value?.enabled ? '内核已停止' : '未启用')
const sourceText = computed(() => state.value?.source.error || (state.value?.source.available ? `已读取 ${state.value.source.groups} 个分组、${state.value.source.nodes} 个节点。只读同步，保留 v2rayN 原始数据。` : '启动时自动读取本机 v2rayN，支持手动指定数据库。'))
const clone = (value: api.AIProxySettings) => new aiproxy.Settings(JSON.parse(JSON.stringify(value)))
function setDraft(settings: api.AIProxySettings) { form.value = clone(settings); domainText.value = Object.fromEntries(form.value.routing.rules.map(rule => [rule.id, rule.domains.join('\n')])) }
function setDomains(rule: proxyRouter.Rule, event: Event) { const raw = (event.target as HTMLTextAreaElement).value; domainText.value[rule.id] = raw; rule.domains = [...new Set(raw.split(/[\s,]+/).filter(Boolean).map(d => d.toLowerCase()))] }
function setPort(rule: proxyRouter.Rule, event: Event) { rule.port = Number((event.target as HTMLInputElement).value) || 0 }
function setTarget(rule: proxyRouter.Rule, event: Event) { const value = (event.target as HTMLSelectElement).value; rule.mode = value === '@direct' ? 'direct' : 'proxy'; rule.node = value === '@direct' ? '' : value }
function addRule() {
  if (!form.value) return
  const id = `rule-${globalThis.crypto?.randomUUID?.() || `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`}`
  form.value.routing.rules.push(new proxyRouter.Rule({ id, name: '新服务', enabled: true, domains: [], mode: 'proxy', node: '', port: 0 }))
  domainText.value[id] = ''
}
function removeRule(id: string) { if (form.value) form.value.routing.rules = form.value.routing.rules.filter(rule => rule.id !== id); delete domainText.value[id] }
function moveRule(index: number, offset: number) { const rules = form.value?.routing.rules; const next = index + offset; if (!rules || next < 0 || next >= rules.length) return; const [rule] = rules.splice(index, 1); rules.splice(next, 0, rule) }
async function load() { const data = await api.getAIProxyState(); if (!form.value) setDraft(data.settings); state.value = data }
async function task(action: () => Promise<void>) { if (busy.value) return; busy.value = true; error.value = ''; try { await action() } catch (e) { error.value = String(e instanceof Error ? e.message : e); await load().catch(() => {}) } finally { busy.value = false } }
async function saveDraft() { if (!form.value) return; state.value = await api.saveAIProxySettings(form.value); setDraft(state.value.settings) }
const save = () => task(async () => { await saveDraft(); showSuccess('代理配置已保存，应用后生效') })
const apply = () => task(async () => { await saveDraft(); state.value = await api.startAIProxy(); showSuccess('AI 代理分流已启用，新建会话自动接入') })
const stop = () => task(async () => { state.value = await api.stopAIProxy(); showSuccess('AI 代理分流已停止') })
const sync = () => task(async () => { await saveDraft(); state.value = await api.syncAIProxyV2rayN(); showSuccess('已同步 v2rayN 节点') })
const refresh = (id: string) => task(async () => { state.value = await api.refreshAIProxySubscription(id); showSuccess('订阅已更新，应用后生效') })
const remove = (id: string) => task(async () => { state.value = await api.deleteAIProxySubscription(id); if (form.value) for (const rule of form.value.routing.rules) if (rule.node.startsWith(`${id}:`)) rule.node = ''; showSuccess('分组已移除，v2rayN 原数据保留') })
const importNodes = () => task(async () => { state.value = await api.importAIProxySubscription(importForm.value.name, importForm.value.url, importForm.value.content); importForm.value = { name: '', url: '', content: '' }; showSuccess('订阅节点已导入') })
let timer: ReturnType<typeof setInterval> | undefined
onActivated(() => { void load().catch(e => { error.value = String(e) }); timer = setInterval(() => { if (!busy.value) void load().catch(() => {}) }, 3000) })
onDeactivated(() => { clearInterval(timer); timer = undefined })
</script>

<style scoped>
.ai-proxy-page{flex:1;display:flex;flex-direction:column;gap:20px;padding:32px 36px;overflow:auto;max-width:1400px;width:100%;margin:0 auto}.page-top,.feature-bar,.section-title,.route-title,.group-row{display:flex;align-items:center;justify-content:space-between;gap:16px}.feature-bar{padding:0 2px}.hint{font-size:12px;color:var(--secondary);line-height:1.7;margin:5px 0}.run-status{font-size:12px;background:var(--control);color:var(--secondary);padding:6px 12px;border-radius:20px;white-space:nowrap}.run-status.running{color:var(--success-strong);background:color-mix(in srgb,var(--success) 12%,transparent)}.run-status.error{color:var(--danger-strong)}h2{font-size:15px;font-weight:600;color:var(--label)}strong{font-size:13px;font-weight:600;color:var(--label)}.actions{display:flex;gap:8px;flex-shrink:0}.route-grid{display:grid;grid-template-columns:1fr 1fr;gap:20px}.route-title h2{display:flex;align-items:center;gap:9px}.route-dot{height:8px;width:8px;border-radius:50%;background:var(--accent)}.route-dot.claude{background:var(--warning)}code{font:11px var(--mono);color:var(--secondary)}label{font-size:12px;color:var(--secondary);display:block}.sel,.input,textarea{display:block;width:100%;margin-top:6px;padding:9px 11px;background:var(--control);border:1px solid transparent;border-radius:8px;color:var(--label);font:13px inherit;outline:none}.sel:focus,.input:focus,textarea:focus{border-color:var(--accent)}textarea{font-family:var(--mono);font-size:12px;line-height:1.7;resize:vertical}.sel{margin-top:-8px;margin-bottom:2px}.rule-head{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:8px}.rule-number{color:var(--tertiary);font-size:12px}.rule-name{margin:0;padding:7px 9px;font-weight:600}.rule-toggle{display:flex;gap:5px;align-items:center;white-space:nowrap}.rule-actions{justify-content:flex-end;margin-top:-8px}.rule-disabled{border:1px dashed var(--separator);box-shadow:none}.pending{font-size:12px;color:var(--warning-strong);margin-top:-10px}.group-row{padding:10px 0;border-top:1px solid color-mix(in srgb,var(--separator) 45%,transparent)}.group-row .hint{margin:2px 0}.empty{font-size:13px;color:var(--secondary);padding:10px 0}.import{margin-bottom:12px}.import-fields{display:grid;grid-template-columns:1fr 2fr;gap:16px}.import form{display:flex;flex-direction:column;gap:12px;padding-top:14px}.import form .btn{align-self:flex-start}summary{cursor:pointer;font-size:13px;color:var(--label);font-weight:500}.integration-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:24px}.advanced-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin-top:18px}details>label{margin-top:14px}pre{background:var(--control);color:var(--secondary);font:11px/1.7 var(--mono);padding:14px;border-radius:8px;white-space:pre-wrap;overflow:auto;max-height:260px;margin:14px 0 10px}@media(max-width:1050px){.feature-bar{align-items:flex-start;flex-direction:column}.integration-grid{grid-template-columns:1fr;gap:8px}}@media(max-width:760px){.ai-proxy-page{padding:24px 20px}.route-grid{grid-template-columns:1fr}.advanced-grid,.import-fields{grid-template-columns:1fr}.route-title{align-items:flex-start;flex-direction:column;gap:5px}}
</style>
