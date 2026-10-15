<template>
  <section ref="pageEl" class="ai-proxy-page" :class="{ 'card-dragging': cardDragging }">
    <header class="page-top">
      <PageHead title="AI 代理分流" description="关联 CodeBox 服务提供商，自动生成域名规则并选择出口。" />
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
      <div class="section-title"><div><h2>服务分流规则</h2><p class="hint">从上到下匹配，首个命中生效；拖动卡片可调整顺序。域名包含其子域名；专用入口使用所属出口，本地回环优先直连。</p></div><AppButton :disabled="busy" @click="addRule">添加规则</AppButton></div>
      <div class="provider-add"><select v-model="providerToAdd" class="input" aria-label="添加已配置的服务提供商" :disabled="busy"><option value="">选择 CodeBox 中已配置的服务提供商</option><option v-for="provider in availableProviders" :key="provider.id" :value="provider.id" :disabled="!provider.domains.length">{{ provider.name }}{{ provider.domains.length ? '' : '（请先配置 API 地址）' }}</option></select><AppButton :disabled="busy || !providerToAdd" @click="addProvider">关联并添加</AppButton></div>
      <p class="hint provider-help">关联后自动读取已启用 API 格式的地址；地址变更后点击“应用更改”生效。登录服务及额外域名可继续使用手动规则。</p>
      <p v-for="issue in state.issues" :key="issue" class="pending">{{ issue }}</p>
      <p v-if="!form.routing.rules.length" class="empty">尚无服务规则，所有请求使用其他域名的默认策略。</p>
      <div class="route-grid">
        <article
          v-for="(rule, index) in form.routing.rules"
          :key="rule.id"
          :data-rule-id="rule.id"
          class="rule-card"
          :class="{
            'rule-disabled': !rule.enabled,
            'is-dragging': cardDragging && dragCardId === rule.id,
            'is-drop-target': cardDragging && dropTargetId === rule.id && dragCardId !== rule.id,
          }"
        >
          <header class="rule-head">
            <button
              type="button"
              class="drag-handle"
              :disabled="busy"
              title="拖拽调整顺序（聚焦后也可用 ↑/↓ 键）"
              :aria-label="`调整规则 ${index + 1} 顺序`"
              @pointerdown="startCardDrag(rule.id, $event)"
              @keydown.up.prevent="moveRule(index, -1)"
              @keydown.down.prevent="moveRule(index, 1)"
            >
              <svg viewBox="0 0 10 16" width="10" height="16" fill="currentColor" aria-hidden="true"><circle cx="2.5" cy="2.5" r="1.6" /><circle cx="7.5" cy="2.5" r="1.6" /><circle cx="2.5" cy="8" r="1.6" /><circle cx="7.5" cy="8" r="1.6" /><circle cx="2.5" cy="13.5" r="1.6" /><circle cx="7.5" cy="13.5" r="1.6" /></svg>
            </button>
            <span class="rule-number" aria-hidden="true">{{ index + 1 }}</span>
            <input v-model="rule.name" class="input rule-name" :aria-label="`规则 ${index + 1} 名称`" maxlength="80" placeholder="服务名称" :disabled="busy" />
            <span class="rule-switch-caption">启用</span>
            <Switch v-model="rule.enabled" :disabled="busy" :aria-label="`启用规则 ${index + 1}`" />
            <button type="button" class="rule-remove" :disabled="busy" :aria-label="`移除规则 ${index + 1}`" @click="removeRule(rule.id)">移除</button>
          </header>
          <label class="rule-field" :for="`${rule.id}-provider`">
            <span class="rule-field-label">关联服务提供商</span>
            <select :id="`${rule.id}-provider`" :value="rule.provider || ''" class="input" :disabled="busy" @change="linkProvider(rule, ($event.target as HTMLSelectElement).value)"><option value="">手动填写域名</option><option v-for="provider in state.providers" :key="provider.id" :value="provider.id">{{ provider.name }}</option><option v-if="rule.provider && !providerByID(rule.provider)" :value="rule.provider">{{ rule.provider }}（已删除或更名，请重新关联）</option></select>
          </label>
          <p v-for="warning in providerByID(rule.provider)?.warnings || []" :key="warning" class="hint provider-warning">{{ warning }}</p>
          <p v-if="rule.provider && !rule.domains.length" class="hint provider-warning">关联来源缺少有效 API 地址，请补全地址、重新关联或停用规则。</p>
          <label class="rule-field" :for="`${rule.id}-node`">
            <span class="rule-field-label">出口方式与节点</span>
            <select :id="`${rule.id}-node`" :value="rule.mode === 'direct' ? '@direct' : rule.node" class="input" :disabled="busy" @change="setTarget(rule, $event)">
              <option value="">请选择代理节点</option><option value="@direct">直连</option>
              <optgroup v-for="group in state.groups" :key="group.id" :label="group.name"><option v-for="node in group.nodes" :key="node.id" :value="node.id">{{ node.name }} · {{ node.type.toUpperCase() }}</option></optgroup>
              <option v-if="rule.mode === 'proxy' && missing(rule.node)" :value="rule.node">原节点已失效，请重新选择</option>
            </select>
          </label>
          <label class="rule-field" :for="`${rule.id}-domains`">
            <span class="rule-field-label">匹配域名</span>
            <textarea :id="`${rule.id}-domains`" :value="domainText[rule.id] ?? rule.domains.join('\n')" rows="3" spellcheck="false" placeholder="api.example.com，每行一个域名" :disabled="busy" :readonly="!!rule.provider" @input="setDomains(rule, $event)" />
          </label>
          <div class="rule-foot">
            <label class="rule-field rule-port" :for="`${rule.id}-port`">
              <span class="rule-field-label">专用端口（可选）</span>
              <input :id="`${rule.id}-port`" :value="rule.port || ''" class="input" type="number" min="1024" max="65535" placeholder="留空：仅使用统一入口" :disabled="busy" @input="setPort(rule, $event)" />
            </label>
            <p v-if="rule.port" class="hint port-hint">专用入口：127.0.0.1:{{ rule.port }}</p>
          </div>
        </article>
      </div>
      <p v-if="!canApply" class="hint">请为已启用的规则填写域名或专用端口，并选择有效代理节点或直连。</p>
      <ConfigCard>
        <h2>TUN 与出口绑定</h2>
        <div class="network-grid"><label>出口方式<select v-model="form.routing.egress_mode" class="input" :disabled="busy"><option value="auto">自动绑定默认网卡（推荐）</option><option value="interface">指定物理网卡</option><option value="system">跟随系统路由</option></select></label><label v-if="form.routing.egress_mode === 'interface'">物理出口网卡<select v-model="form.routing.bind_interface" class="input" :disabled="busy"><option value="">请选择已连接的网卡</option><option v-for="nic in state.interfaces.filter(n => n.selectable)" :key="nic.name" :value="nic.name">{{ nic.name }}</option><option v-if="form.routing.bind_interface && !state.interfaces.some(n => n.name === form?.routing.bind_interface && n.selectable)" :value="form.routing.bind_interface">{{ form.routing.bind_interface }}（不可用）</option></select></label><label>独立 DNS（可选）<input v-model.trim="form.routing.dns_server" class="input" :disabled="busy" placeholder="DNS 服务器 IP，留空使用系统解析" /></label></div>
        <p v-if="form.routing.egress_mode === 'system'" class="hint provider-warning">当前跟随系统路由，v2rayN TUN 可能再次接管出口。</p>
        <p v-else class="hint">将本功能的节点连接和直连流量绑定至出口网卡，绕开常见 TUN 路由。自动模式异常时可指定物理网卡；不会修改 v2rayN 设置。</p>
        <p class="hint">系统 DNS 仍可能经过 TUN；若启用了 FakeIP，可指定独立 DNS。若出口节点本身是 127.0.0.1 / localhost 代理，后续路径仍由该代理决定。本地 API 与本地中继始终回环直连。</p>
      </ConfigCard>
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
        <div class="integration-grid"><div><strong>新建会话自动生效</strong><p class="hint">内嵌、外部会话及宿主远程启动统一接入。Pi / OMP 的多模型与子 Agent 按请求域名分别分流。</p></div><div><strong>国内模型默认直连</strong><p class="hint">GLM 等未匹配域名走直连。第三方 API 可直接关联已有服务提供商，也可手动补充域名。</p></div><div><strong>压缩与模型探测一致</strong><p class="hint">新启动的 Headroom、提交总结模型、多模态与额度探测使用相同分流。已有会话和 Headroom 需重启后接入。</p></div></div>
        <p class="hint">当前管理的是本机。远程宿主使用宿主自己的配置；WSL 回环转发暂不支持。TUN 的出口策略可在上方独立管理。</p>
      </ConfigCard>
      <ConfigCard><details><summary>高级设置与运行日志</summary><label>统一入口端口<input v-model.number="form.routing.proxy_port" class="input" type="number" min="1024" max="65535" /></label><label>其他域名上游（留空直连）<input v-model="form.routing.fallback_proxy" class="input" autocomplete="off" placeholder="留空：国内模型及其他域名直连" /></label><label>订阅下载代理（留空直连）<input v-model="form.routing.fetch_proxy" class="input" autocomplete="off" placeholder="http://127.0.0.1:5800" /></label><label>sing-box 路径（留空自动查找）<input v-model="form.corePath" class="input" :placeholder="state.status.core || '自动查找 v2rayN 内核或 PATH'" /></label><label>v2rayN 数据库（留空自动查找）<input v-model="form.v2raynDatabase" class="input" :placeholder="state.source.database || 'guiConfigs/guiNDB.db'" /></label><p class="hint">统一入口：127.0.0.1:{{ form.routing.proxy_port }}。所有入口仅监听本机；停止分流后，新会话恢复原有代理策略。</p><pre>{{ state.status.error || state.status.logs || '暂无运行日志' }}</pre></details></ConfigCard>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onActivated, onDeactivated, onUnmounted, ref } from 'vue'
import { aiproxy, router as proxyRouter } from '../../wailsjs/go/models'
import * as api from '../api/aiProxy'
import { dropTargetIdOf, moveItemById, passesDragThreshold, type DragStartPoint } from './aiProxyRuleDrag'
import PageHead from '../components/ui/PageHead.vue'
import ConfigCard from '../components/ui/ConfigCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import Switch from '../components/ui/Switch.vue'
import StatusBanner from '../components/ui/StatusBanner.vue'
import { useToast } from '../composables/useToast'

const state = ref<api.AIProxyState | null>(null)
const form = ref<api.AIProxySettings | null>(null)
const domainText = ref<Record<string, string>>({})
const busy = ref(false), error = ref('')
const importForm = ref({ name: '', url: '', content: '' })
const providerToAdd = ref('')
const { showSuccess } = useToast()
const dirty = computed(() => state.value && form.value && JSON.stringify(state.value.settings) !== JSON.stringify(form.value))
const ids = computed(() => new Set(state.value?.groups.flatMap(g => g.nodes.map(n => n.id)) || []))
const providerByID = (id?: string) => state.value?.providers.find(p => p.id === id)
const availableProviders = computed(() => state.value?.providers.filter(p => !form.value?.routing.rules.some(r => r.provider === p.id)) || [])
const missing = (id: string) => !!id && !ids.value.has(id)
const canApply = computed(() => form.value && form.value.routing.rules.every(rule => !rule.enabled || (rule.name.trim() && (rule.provider ? !!providerByID(rule.provider)?.domains.length : (rule.domains.length || rule.port)) && (rule.mode === 'direct' || ids.value.has(rule.node)))))
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
function linkProvider(rule: proxyRouter.Rule, id: string) {
  rule.provider = id
  if (id) { const provider = providerByID(id); rule.domains = [...(provider?.domains || [])]; if (provider) rule.name = provider.name }
  domainText.value[rule.id] = rule.domains.join('\n')
}
function addProvider() {
  const provider = providerByID(providerToAdd.value)
  if (!form.value || !provider?.domains.length || form.value.routing.rules.some(r => r.provider === provider.id)) return
  const id = `rule-${globalThis.crypto.randomUUID()}`
  form.value.routing.rules.unshift(new proxyRouter.Rule({ id, name: provider.name, provider: provider.id, enabled: true, domains: [...provider.domains], mode: 'proxy', node: '', port: 0 }))
  domainText.value[id] = provider.domains.join('\n')
  providerToAdd.value = ''
}
function updateLinkedDomains() {
  if (!form.value) return
  for (const rule of form.value.routing.rules) {
    if (!rule.provider) continue
    rule.domains = [...(providerByID(rule.provider)?.domains || [])]
    domainText.value[rule.id] = rule.domains.join('\n')
  }
}
function removeRule(id: string) { if (form.value) form.value.routing.rules = form.value.routing.rules.filter(rule => rule.id !== id); delete domainText.value[id] }
function moveRule(index: number, offset: number) { const rules = form.value?.routing.rules; const next = index + offset; if (!rules || next < 0 || next >= rules.length) return; const [rule] = rules.splice(index, 1); rules.splice(next, 0, rule) }

// —— 规则卡片拖拽排序：Pointer Events 自绘（WKWebView 不触发 HTML5 DnD），悬停即让位 ——
// 拖拽仅能从手柄发起（startCardDrag 挂 window 监听），卡片内的输入控件不受影响；
// 越过阈值才进入拖拽态，保留手柄点击聚焦；聚焦后 ↑/↓ 仍可键盘排序（见 moveRule）。
let dragStart: DragStartPoint & { id: string } | null = null
const cardDragging = ref(false)
const dragCardId = ref('')
const dropTargetId = ref('')
const pageEl = ref<HTMLElement | null>(null)

function startCardDrag(id: string, event: PointerEvent) {
  if (busy.value || event.button !== 0) return
  dragStart = { id, pointerId: event.pointerId, startX: event.clientX, startY: event.clientY }
  window.addEventListener('pointermove', onCardDragMove)
  window.addEventListener('pointerup', endCardDrag)
  window.addEventListener('pointercancel', endCardDrag)
}
function onCardDragMove(event: PointerEvent) {
  if (!dragStart || event.pointerId !== dragStart.pointerId || !form.value) return
  if (!cardDragging.value) {
    if (!passesDragThreshold(dragStart, event.clientX, event.clientY)) return
    cardDragging.value = true
    dragCardId.value = dragStart.id
  }
  event.preventDefault()
  dropTargetId.value = dropTargetIdOf(cardAtPoint(event.clientX, event.clientY), dragCardId.value)
  if (dropTargetId.value) moveItemById(form.value.routing.rules, dragCardId.value, dropTargetId.value, rule => rule.id)
  autoScrollDuringDrag(event.clientY)
}
function endCardDrag(event: PointerEvent) {
  if (dragStart && event.pointerId !== dragStart.pointerId) return
  stopCardDrag()
}
function stopCardDrag() {
  dragStart = null
  cardDragging.value = false
  dragCardId.value = ''
  dropTargetId.value = ''
  window.removeEventListener('pointermove', onCardDragMove)
  window.removeEventListener('pointerup', endCardDrag)
  window.removeEventListener('pointercancel', endCardDrag)
}
function cardAtPoint(x: number, y: number): string {
  return document.elementFromPoint(x, y)?.closest('.rule-card')?.getAttribute('data-rule-id') || ''
}
function autoScrollDuringDrag(y: number) {
  const container = pageEl.value
  if (!container) return
  const { top, bottom } = container.getBoundingClientRect()
  if (y < top + 48) container.scrollTop -= 14
  else if (y > bottom - 48) container.scrollTop += 14
}
async function load() { const data = await api.getAIProxyState(); state.value = data; if (!form.value) setDraft(data.settings); updateLinkedDomains() }
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
onDeactivated(() => { clearInterval(timer); timer = undefined; stopCardDrag() })
onUnmounted(stopCardDrag)
</script>

<style scoped>
.provider-add{display:flex;gap:10px;align-items:center}.provider-add select{margin:0;max-width:480px}.provider-help{margin-top:-14px}.provider-warning{color:var(--warning-strong)}.network-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:16px}textarea[readonly]{color:var(--secondary)}
.ai-proxy-page{flex:1;display:flex;flex-direction:column;gap:20px;padding:32px 36px;overflow:auto;max-width:1400px;width:100%;margin:0 auto}.page-top,.feature-bar,.section-title,.route-title,.group-row{display:flex;align-items:center;justify-content:space-between;gap:16px}.feature-bar{padding:0 2px}.hint{font-size:12px;color:var(--secondary);line-height:1.7;margin:5px 0}.run-status{font-size:12px;background:var(--control);color:var(--secondary);padding:6px 12px;border-radius:20px;white-space:nowrap}.run-status.running{color:var(--success-strong);background:color-mix(in srgb,var(--success) 12%,transparent)}.run-status.error{color:var(--danger-strong)}h2{font-size:15px;font-weight:600;color:var(--label)}strong{font-size:13px;font-weight:600;color:var(--label)}.actions{display:flex;gap:8px;flex-shrink:0}.route-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:14px}.route-title h2{display:flex;align-items:center;gap:9px}.route-dot{height:8px;width:8px;border-radius:50%;background:var(--accent)}.route-dot.claude{background:var(--warning)}code{font:11px var(--mono);color:var(--secondary)}
/* 规则小卡片：视觉与提供商页 prov-card 对齐（边框卡片 + hover 浮起），拖拽态/放置目标高亮 */
.rule-card{background:var(--card);border:1px solid var(--separator);border-radius:12px;box-shadow:var(--shadow-sm);padding:14px 16px;display:flex;flex-direction:column;gap:10px;transition:box-shadow .15s,border-color .15s,opacity .15s,transform .15s}.rule-card:hover{box-shadow:0 4px 14px rgba(0,0,0,.08)}.rule-card.is-dragging{position:relative;z-index:4;opacity:.6;border-color:var(--accent);transform:scale(.985);box-shadow:0 10px 28px rgba(0,0,0,.16)}.rule-card.is-drop-target{border-color:var(--accent)}.rule-disabled{border-style:dashed;opacity:.72;box-shadow:none}.rule-disabled:hover{box-shadow:none}.rule-head{display:flex;align-items:center;gap:8px}.drag-handle{display:inline-flex;align-items:center;justify-content:center;width:22px;height:26px;padding:0;border:none;background:transparent;border-radius:6px;color:var(--tertiary);cursor:grab;touch-action:none}.drag-handle:hover:not(:disabled){color:var(--secondary);background:var(--control)}.drag-handle:active:not(:disabled){cursor:grabbing}.drag-handle:disabled{opacity:.4;cursor:not-allowed}.rule-number{flex-shrink:0;min-width:22px;text-align:center;padding:2px 6px;border-radius:6px;background:var(--control);color:var(--tertiary);font-size:11px;font-weight:600;font-variant-numeric:tabular-nums}.rule-card .rule-name{flex:1;min-width:0;margin:0;padding:6px 9px;font-weight:600}.rule-switch-caption{flex-shrink:0;font-size:11px;color:var(--tertiary);white-space:nowrap}.rule-remove{flex-shrink:0;border:none;background:transparent;color:var(--tertiary);font-family:inherit;font-size:11px;line-height:1.4;padding:4px 8px;border-radius:5px;cursor:pointer}.rule-remove:hover:not(:disabled){color:#FF3B30;background:rgba(255,59,48,.08)}.rule-remove:disabled{opacity:.4;cursor:not-allowed}.rule-field{display:flex;flex-direction:column;gap:5px}.rule-field-label{font-size:11px;color:var(--tertiary)}.rule-card .input,.rule-card textarea{margin:0}.rule-foot{display:flex;align-items:flex-end;gap:10px;flex-wrap:wrap}.rule-port{flex:0 1 200px}.rule-card .hint{margin:0}.rule-foot .port-hint{margin:0 0 9px;white-space:nowrap}.ai-proxy-page.card-dragging{user-select:none}.ai-proxy-page.card-dragging :deep(*){cursor:grabbing!important}label{font-size:12px;color:var(--secondary);display:block}.input,textarea{display:block;width:100%;margin-top:6px;padding:9px 11px;background:var(--control);border:1px solid transparent;border-radius:8px;color:var(--label);font:13px inherit;outline:none}.input:focus,textarea:focus{border-color:var(--accent)}textarea{font-family:var(--mono);font-size:12px;line-height:1.7;resize:vertical}.pending{font-size:12px;color:var(--warning-strong);margin-top:-10px}.group-row{padding:10px 0;border-top:1px solid color-mix(in srgb,var(--separator) 45%,transparent)}.group-row .hint{margin:2px 0}.empty{font-size:13px;color:var(--secondary);padding:10px 0}.import{margin-bottom:12px}.import-fields{display:grid;grid-template-columns:1fr 2fr;gap:16px}.import form{display:flex;flex-direction:column;gap:12px;padding-top:14px}.import form .btn{align-self:flex-start}summary{cursor:pointer;font-size:13px;color:var(--label);font-weight:500}.integration-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:24px}.advanced-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin-top:18px}details>label{margin-top:14px}pre{background:var(--control);color:var(--secondary);font:11px/1.7 var(--mono);padding:14px;border-radius:8px;white-space:pre-wrap;overflow:auto;max-height:260px;margin:14px 0 10px}@media(max-width:1050px){.feature-bar{align-items:flex-start;flex-direction:column}.integration-grid{grid-template-columns:1fr;gap:8px}}@media(max-width:760px){.ai-proxy-page{padding:24px 20px}.advanced-grid,.import-fields{grid-template-columns:1fr}.route-title{align-items:flex-start;flex-direction:column;gap:5px}}
</style>
