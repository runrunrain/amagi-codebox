<template>
  <section class="ai-proxy-page" :class="{ 'card-dragging': cardDragging }" aria-label="AI 代理分流">
    <div class="workspace-head">
      <header class="page-top">
        <div>
          <div class="eyebrow"><AIProxyIcon name="route" /> 本机会话 · 网络路由</div>
          <PageHead title="AI 代理分流" description="让每个服务，都有明确的出口。" />
        </div>
        <div v-if="state && form" class="actions page-actions">
          <AppButton :disabled="busy || !dirty" @click="save">保存配置</AppButton>
          <AppButton variant="primary" :disabled="busy || !canApply || (state.status.running && !dirty && !state.pending)" @click="apply">
            {{ busy ? '处理中…' : state.status.running ? '应用更改' : '启用分流' }}<AIProxyIcon name="arrow" />
          </AppButton>
        </div>
      </header>
      <StatusBanner v-if="error" type="error" :message="error" />
      <StatusBanner v-if="state?.error" type="error" :message="state.error" />
      <template v-if="state && form">
        <div class="status-strip">
          <div class="status-primary">
            <span class="status-dot" :class="{ running: state.status.running, failed: state.enabled && !state.status.running }" />
            <div><strong>{{ statusText }}</strong><span>{{ state.pending ? '应用配置后可新建会话' : state.status.running ? '新建会话自动接入' : '启用后，新建会话自动接入' }}</span></div>
          </div>
          <div class="status-item"><span>启用规则</span><strong>{{ enabledRuleCount }}<small> / {{ form.routing.rules.length }}</small></strong></div>
          <div class="status-item"><span>可用节点</span><strong>{{ ids.size }}<small> 个</small></strong></div>
          <div class="status-action"><span class="scope-tag">仅本机</span><button v-if="state.status.running" class="text-button" :disabled="busy" @click="stop">停止分流</button></div>
        </div>
        <div v-if="dirty || state.pending" class="pending-banner" role="status">
          <AIProxyIcon name="clock" /><span><strong>{{ dirty ? '有未保存的更改' : '配置已保存，等待应用' }}</strong>应用时会重启代理内核，现有连接将断开。</span>
        </div>
        <div class="workspace-tabs" role="tablist" aria-label="代理管理">
          <button v-for="tab in tabs" :id="`proxy-tab-${tab.id}`" :key="tab.id" role="tab" :aria-selected="activeTab === tab.id" :aria-controls="`proxy-panel-${tab.id}`" :tabindex="activeTab === tab.id ? 0 : -1" :class="{ selected: activeTab === tab.id }" @click="selectTab(tab.id)" @keydown="onTabKey($event, tab.id)">{{ tab.label }}</button>
        </div>
      </template>
    </div>
    <div v-if="!state" class="page-loading" role="status"><AIProxyIcon name="route" />{{ error ? '暂时无法读取代理配置' : '正在读取代理配置…' }}<AppButton v-if="error" @click="retryLoad">重新加载</AppButton></div>
    <div v-if="state && form" ref="pageEl" class="proxy-content">
      <section v-show="activeTab === 'rules'" id="proxy-panel-rules" class="tab-panel" role="tabpanel" aria-labelledby="proxy-tab-rules" tabindex="0">
        <div class="section-title">
          <div><h2>服务与出口 <span class="count">{{ form.routing.rules.length }}</span></h2><p class="hint">按域名匹配服务，未匹配的请求{{ form.routing.fallback_proxy ? '使用默认上游' : '默认直连' }}。</p></div>
          <AppButton :disabled="busy" :aria-expanded="showAdd" aria-controls="proxy-add-rule" @click="showAdd = !showAdd"><AIProxyIcon name="plus" /> 添加规则</AppButton>
        </div>
        <div v-if="showAdd" id="proxy-add-rule" class="add-panel">
          <div><strong>从已有服务提供商添加</strong><p class="hint">自动关联 API 域名，地址更新后可重新应用。</p></div>
          <div class="provider-add">
            <select v-model="providerToAdd" class="input" aria-label="添加已配置的服务提供商" :disabled="busy"><option value="">选择服务提供商</option><option v-for="provider in availableProviders" :key="provider.id" :value="provider.id" :disabled="!provider.domains.length">{{ provider.name }}{{ provider.domains.length ? '' : '（请先配置 API 地址）' }}</option></select>
            <AppButton variant="primary" :disabled="busy || !providerToAdd" @click="addProvider">关联并添加</AppButton>
            <span class="or-divider">或</span><button class="text-button" :disabled="busy" @click="addRule">手动添加域名</button>
          </div>
        </div>
        <div v-if="state.issues.length" class="notice warning"><AIProxyIcon name="alert" /><div><p v-for="issue in state.issues" :key="issue">{{ issue }}</p></div></div>
        <div v-if="!form.routing.rules.length" class="empty-state"><AIProxyIcon name="route" /><h3>为第一个服务选择出口</h3><p>关联 CodeBox 提供商，或手动添加需要分流的域名。</p><AppButton :disabled="busy" @click="showAdd = true">添加服务规则</AppButton></div>
        <div v-else class="route-list">
          <div class="route-columns" aria-hidden="true"><span>服务 / 匹配域名</span><span>出口节点</span><span>启用</span></div>
          <article v-for="(rule, index) in form.routing.rules" :key="rule.id" :data-rule-id="rule.id" class="rule-card" :class="{ 'rule-disabled': !rule.enabled, expanded: expandedRule === rule.id, 'is-dragging': cardDragging && dragCardId === rule.id, 'is-drop-target': cardDragging && dropTargetId === rule.id && dragCardId !== rule.id }">
            <div class="rule-summary">
              <button type="button" class="drag-handle" :disabled="busy" title="拖动排序，或使用 ↑ / ↓ 键" :aria-label="`调整规则 ${index + 1} 顺序`" @pointerdown="startCardDrag(rule.id, $event)" @keydown.up.prevent="moveRule(index, -1)" @keydown.down.prevent="moveRule(index, 1)"><svg viewBox="0 0 10 16" width="10" height="16" fill="currentColor" aria-hidden="true"><circle cx="2.5" cy="2.5" r="1.3" /><circle cx="7.5" cy="2.5" r="1.3" /><circle cx="2.5" cy="8" r="1.3" /><circle cx="7.5" cy="8" r="1.3" /><circle cx="2.5" cy="13.5" r="1.3" /><circle cx="7.5" cy="13.5" r="1.3" /></svg></button>
              <button type="button" class="rule-identity" :aria-label="`编辑规则 ${rule.name || index + 1}`" :aria-expanded="expandedRule === rule.id" :aria-controls="`${rule.id}-editor`" @click="toggleRule(rule.id)">
                <span class="service-icon" :class="{ linked: rule.provider }" aria-hidden="true">{{ (rule.name || '?').slice(0, 1).toUpperCase() }}</span>
                <span class="service-copy"><span class="service-title"><strong>{{ rule.name || '未命名服务' }}</strong><span v-if="ruleIssue(rule)" class="rule-warning">待完善</span><span v-else-if="!rule.enabled" class="muted-label">已停用</span></span><span class="domain-preview" :title="rule.domains.join(' · ')">{{ rule.domains.slice(0, 2).join(' · ') || (rule.port ? `专用端口 ${rule.port}` : '尚未添加域名') }}<span v-if="rule.domains.length > 2" class="more-domains"> +{{ rule.domains.length - 2 }}</span></span></span>
              </button>
              <div class="route-target" :class="{ incomplete: rule.enabled && rule.mode !== 'direct' && (!rule.node || missing(rule.node)) }"><AIProxyIcon :name="rule.mode === 'direct' ? 'globe' : 'arrow'" /><select :id="`${rule.id}-node`" :value="rule.mode === 'direct' ? '@direct' : rule.node" class="input" :aria-label="`${rule.name || `规则 ${index + 1}`} 出口节点`" :disabled="busy" @change="setTarget(rule, $event)"><option value="">选择出口节点</option><option value="@direct">直连</option><optgroup v-for="group in state.groups" :key="group.id" :label="group.name"><option v-for="node in group.nodes" :key="node.id" :value="node.id">{{ node.name }} · {{ node.type.toUpperCase() }}</option></optgroup><option v-if="rule.mode === 'proxy' && missing(rule.node)" :value="rule.node">原节点已失效，请重新选择</option></select></div>
              <Switch v-model="rule.enabled" :disabled="busy" :aria-label="`启用规则 ${index + 1}`" />
              <button class="expand-button" :aria-label="`${expandedRule === rule.id ? '收起' : '展开'}规则 ${index + 1}`" :aria-expanded="expandedRule === rule.id" :aria-controls="`${rule.id}-editor`" @click="toggleRule(rule.id)"><AIProxyIcon name="chevron" /></button>
            </div>
            <div v-if="expandedRule === rule.id" :id="`${rule.id}-editor`" class="rule-editor">
              <div class="editor-fields">
                <label class="field">服务名称<input v-model="rule.name" class="input" :aria-label="`规则 ${index + 1} 名称`" maxlength="80" placeholder="服务名称" :disabled="busy" /></label>
                <label class="field">关联服务提供商<select :id="`${rule.id}-provider`" :value="rule.provider || ''" class="input" :disabled="busy" @change="linkProvider(rule, ($event.target as HTMLSelectElement).value)"><option value="">手动填写域名</option><option v-for="provider in state.providers" :key="provider.id" :value="provider.id">{{ provider.name }}</option><option v-if="rule.provider && !providerByID(rule.provider)" :value="rule.provider">{{ rule.provider }}（已删除或更名）</option></select></label>
                <label class="field field-wide"><span>匹配域名 <span class="field-note">{{ rule.provider ? '从提供商自动同步' : '包含子域名，每行一个' }}</span></span><textarea :id="`${rule.id}-domains`" :value="domainText[rule.id] ?? rule.domains.join('\n')" rows="3" spellcheck="false" placeholder="api.example.com" :disabled="busy" :readonly="!!rule.provider" @input="setDomains(rule, $event)" /></label>
                <label class="field">专用端口 <span class="field-note">可选</span><input :id="`${rule.id}-port`" :value="rule.port || ''" class="input" type="number" min="1024" max="65535" placeholder="留空，使用统一入口" :disabled="busy" @input="setPort(rule, $event)" /></label>
                <div class="editor-aside hint">{{ rule.port ? `专用入口：127.0.0.1:${rule.port}` : '使用统一入口，按请求域名分流。' }}<br />本地回环地址始终直连。</div>
              </div>
              <p v-for="warning in providerByID(rule.provider)?.warnings || []" :key="warning" class="hint warning-text">{{ warning }}</p>
              <p v-if="ruleIssue(rule)" class="hint warning-text">{{ ruleIssue(rule) }}</p>
              <div class="editor-footer"><button class="text-button danger-text" :disabled="busy" :aria-label="`移除规则 ${index + 1}`" @click="removeRule(rule.id)">移除此规则</button><AppButton size="small" @click="toggleRule(rule.id)">完成编辑</AppButton></div>
            </div>
          </article>
        </div>
        <p v-if="form.routing.rules.length" class="list-footnote">拖动左侧手柄调整优先级，点击服务展开编辑。重叠域名不可指定不同出口。</p>
        <p v-if="!canApply" class="notice warning"><AIProxyIcon name="alert" />完善标记的规则后，即可应用分流配置。</p>
        <details class="help-disclosure"><summary><AIProxyIcon name="info" />会话如何接入分流？<AIProxyIcon name="chevron" /></summary><div class="help-body"><p>启用后，从 CodeBox 新建的 Pi、Claude Code 等本机会话自动接入。Pi / OMP 的多模型请求按各自域名分流；已有会话和 Headroom 需重启后接入。</p><p>启用期间，配置未应用、内核停止或入口不可达时会阻止新建会话。此保障面向遵守代理设置的客户端，不等同于对任意工具子进程进行网络隔离。</p><p>远程宿主使用宿主自己的配置。WSL 会话需要 WSL2 mirrored 网络模式；本地 API 与中继保持回环直连。</p></div></details>
      </section>
      <section v-show="activeTab === 'traffic'" id="proxy-panel-traffic" class="tab-panel" role="tabpanel" aria-labelledby="proxy-tab-traffic" tabindex="0"><AIProxyTraffic v-if="trafficVisited" :active="activeTab === 'traffic'" /></section>
      <section v-show="activeTab === 'settings'" id="proxy-panel-settings" class="tab-panel settings-panel" role="tabpanel" aria-labelledby="proxy-tab-settings" tabindex="0">
        <AIProxySettingsPanel :state="state" :form="form" :busy="busy" :import-form="importForm" @sync="sync" @refresh="refresh" @remove="remove" @import="importNodes" />
      </section>
    </div>
  </section>
</template>


<script setup lang="ts">
import { computed, nextTick, onActivated, onDeactivated, onUnmounted, ref } from 'vue'
import { aiproxy, router as proxyRouter } from '../../wailsjs/go/models'
import * as api from '../api/aiProxy'
import { dropTargetIdOf, moveItemById, passesDragThreshold, type DragStartPoint } from './aiProxyRuleDrag'
import AIProxyTraffic from '../components/AIProxyTraffic.vue'
import AIProxyIcon from '../components/AIProxyIcon.vue'
import AIProxySettingsPanel from '../components/AIProxySettingsPanel.vue'
import PageHead from '../components/ui/PageHead.vue'
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
const tabs = [{ id: 'rules', label: '分流规则' }, { id: 'traffic', label: '连接日志' }, { id: 'settings', label: '节点与设置' }] as const
type ProxyTab = typeof tabs[number]['id']
const activeTab = ref<ProxyTab>('rules')
const trafficVisited = ref(false)
const showAdd = ref(false)
const expandedRule = ref('')
const tabScroll: Record<ProxyTab, number> = { rules: 0, traffic: 0, settings: 0 }
function selectTab(id: ProxyTab) {
  if (activeTab.value === id) return
  if (pageEl.value) tabScroll[activeTab.value] = pageEl.value.scrollTop
  stopCardDrag()
  activeTab.value = id
  if (id === 'traffic') trafficVisited.value = true
  void nextTick(() => { if (pageEl.value) pageEl.value.scrollTop = tabScroll[id] })
}
function onTabKey(event: KeyboardEvent, id: ProxyTab) {
  const index = tabs.findIndex(tab => tab.id === id)
  const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1
  if (next < 0) return
  event.preventDefault()
  selectTab(tabs[next].id)
  document.getElementById(`proxy-tab-${tabs[next].id}`)?.focus()
}
function toggleRule(id: string) {
  const editor = document.getElementById(`${id}-editor`)
  const row = editor?.closest('.rule-card')
  const restoreFocus = expandedRule.value === id && editor?.contains(document.activeElement)
  expandedRule.value = expandedRule.value === id ? '' : id
  if (restoreFocus) void nextTick(() => row?.querySelector<HTMLButtonElement>('.rule-identity')?.focus())
}
function focusRule(id: string) {
  expandedRule.value = id
  showAdd.value = false
  void nextTick(() => document.getElementById(`${id}-editor`)?.querySelector<HTMLInputElement>('input')?.focus())
}
const { showSuccess } = useToast()
const dirty = computed(() => state.value && form.value && JSON.stringify(new aiproxy.Settings(state.value.settings)) !== JSON.stringify(form.value))
const ids = computed(() => new Set(state.value?.groups.flatMap(g => g.nodes.map(n => n.id)) || []))
const providerByID = (id?: string) => state.value?.providers.find(p => p.id === id)
const availableProviders = computed(() => state.value?.providers.filter(p => !form.value?.routing.rules.some(r => r.provider === p.id)) || [])
const missing = (id: string) => !!id && !ids.value.has(id)
const enabledRuleCount = computed(() => form.value?.routing.rules.filter(rule => rule.enabled).length || 0)
function ruleIssue(rule: proxyRouter.Rule): string {
  if (!rule.enabled) return ''
  if (!rule.name.trim()) return '请填写服务名称。'
  if (rule.provider ? !providerByID(rule.provider)?.domains.length : !rule.domains.length && !rule.port) return rule.provider ? '关联来源缺少有效 API 地址，请补全地址、重新关联或停用规则。' : '请填写匹配域名或专用端口。'
  if (rule.mode !== 'direct' && !ids.value.has(rule.node)) return '请选择可用的出口节点，或使用直连。'
  return ''
}
const canApply = computed(() => form.value && form.value.routing.rules.every(rule => !ruleIssue(rule)))
const statusText = computed(() => state.value?.status.running ? (state.value.pending ? '运行中 · 待应用' : '分流运行中') : state.value?.enabled ? '内核已停止' : '未启用')
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
  focusRule(id)
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
  focusRule(id)
}
function updateLinkedDomains() {
  if (!form.value) return
  for (const rule of form.value.routing.rules) {
    if (!rule.provider) continue
    rule.domains = [...(providerByID(rule.provider)?.domains || [])]
    domainText.value[rule.id] = rule.domains.join('\n')
  }
}
function removeRule(id: string) { if (form.value) form.value.routing.rules = form.value.routing.rules.filter(rule => rule.id !== id); delete domainText.value[id]; if (expandedRule.value === id) expandedRule.value = '' }
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
const retryLoad = () => { error.value = ''; void load().catch(e => { error.value = String(e) }) }
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

<style scoped src="../styles/ai-proxy-controls.css"></style>
<style scoped>
.ai-proxy-page {
  --proxy-border: color-mix(in srgb, var(--separator) 62%, transparent);
  flex: 1; min-width: 0; min-height: 0; width: 100%; max-width: 1440px; margin: 0 auto;
  display: flex; flex-direction: column; overflow: hidden; color: var(--label); background: var(--window);
}
.workspace-head { flex-shrink: 0; padding: 32px 36px 0; display: flex; flex-direction: column; gap: 20px; }
.page-top { display: flex; align-items: center; justify-content: space-between; gap: 24px; }
.eyebrow { display: flex; align-items: center; gap: 6px; margin-bottom: 10px; font-size: 11px; color: var(--secondary); letter-spacing: .5px; }
.eyebrow svg { width: 15px; height: 15px; }
.page-top :deep(h1) { font-size: 27px; letter-spacing: -.7px; }
.page-top :deep(.page-head p) { margin-top: 7px; color: var(--secondary); }
.page-actions .btn { min-height: 38px; white-space: nowrap; }
.page-actions svg { width: 15px; height: 15px; margin-left: 3px; }
.status-strip { display: flex; align-items: center; gap: 28px; padding: 18px 22px; border: 1px solid var(--proxy-border); border-radius: 12px; background: var(--card); }
.status-primary { display: flex; align-items: center; gap: 12px; min-width: 190px; flex: 1; }
.status-primary > div { display: flex; flex-direction: column; gap: 5px; }
.status-primary span:not(.status-dot) { font-size: 11px; color: var(--secondary); }
.status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--tertiary); flex-shrink: 0; }
.status-dot.running { background: var(--success-strong); box-shadow: 0 0 0 4px color-mix(in srgb, var(--success) 10%, transparent); }
.status-dot.failed { background: var(--danger-strong); }
.status-item { display: flex; flex-direction: column; gap: 5px; min-width: 84px; }
.status-item > span { font-size: 11px; color: var(--secondary); }
.status-item > strong { font-size: 18px; font-weight: 600; font-variant-numeric: tabular-nums; }
.status-item small { font-size: 11px; font-weight: 400; color: var(--secondary); }
.status-action { display: flex; align-items: center; gap: 15px; }
.scope-tag { font-size: 10px; letter-spacing: .5px; color: var(--secondary); padding: 3px 7px; background: var(--control); border-radius: 5px; white-space: nowrap; }
.pending-banner { display: flex; align-items: center; gap: 10px; color: var(--warning-strong); font-size: 12px; line-height: 1.7; margin: -6px 0; }
.pending-banner strong { font-size: 12px; margin-right: 12px; color: inherit; }
.workspace-tabs { display: flex; gap: 28px; border-bottom: 1px solid var(--proxy-border); }
.workspace-tabs button { position: relative; border: none; padding: 0 0 14px; background: none; color: var(--secondary); font: inherit; font-size: 13px; cursor: pointer; }
.workspace-tabs button::after { content: ''; position: absolute; height: 2px; left: 0; right: 0; bottom: -1px; background: transparent; border-radius: 3px; transition: background .18s; }
.workspace-tabs button:hover { color: var(--label); }
.workspace-tabs button.selected { font-weight: 600; color: var(--accent-strong); }
.workspace-tabs button.selected::after { background: var(--accent); }
.proxy-content { overflow: auto; flex: 1; min-height: 0; padding: 24px 36px 36px; scroll-padding: 16px; }
.tab-panel { min-width: 0; }
.tab-panel:focus-visible { outline-offset: -2px; }
.tab-panel > .section-title { margin-bottom: 20px; }
.count { display: inline-flex; align-items: center; justify-content: center; min-width: 24px; padding: 1px 6px; margin-left: 6px; background: var(--control); border-radius: 6px; font-size: 11px; color: var(--secondary); vertical-align: 1px; }
.route-list { border: 1px solid var(--proxy-border); border-radius: 12px; overflow: hidden; background: var(--card); }
.route-columns { display: grid; grid-template-columns: minmax(0, 1fr) minmax(190px, 30%) 88px; gap: 18px; padding: 11px 20px 11px 54px; background: color-mix(in srgb, var(--control) 55%, var(--card)); color: var(--secondary); font-size: 11px; }
.rule-card { position: relative; border-top: 1px solid var(--proxy-border); transition: background .15s, opacity .15s; }
.rule-card:hover { background: color-mix(in srgb, var(--control) 28%, var(--card)); }
.rule-card.expanded { background: color-mix(in srgb, var(--control) 28%, var(--card)); }
.rule-summary { display: grid; grid-template-columns: 18px minmax(0, 1fr) minmax(190px, 30%) 44px 26px; align-items: center; gap: 18px; padding: 21px 20px 21px 16px; }
.rule-identity { display: flex; align-items: center; gap: 13px; min-width: 0; padding: 0; border: 0; background: none; text-align: left; color: inherit; font: inherit; cursor: pointer; }
.service-icon { display: grid; place-items: center; flex-shrink: 0; width: 38px; height: 38px; border-radius: 11px; border: 1px solid var(--proxy-border); background: var(--card); color: var(--secondary); font-size: 16px; font-weight: 500; }
.service-icon.linked { color: var(--accent-strong); background: color-mix(in srgb, var(--accent) 5%, var(--card)); border-color: color-mix(in srgb, var(--accent) 12%, transparent); }
.service-copy { min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.service-title { display: flex; align-items: center; gap: 8px; min-width: 0; }
.service-title strong { font-size: 14px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.domain-preview { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font: 11px/1.6 var(--mono); color: var(--secondary); }
.more-domains { color: var(--accent-strong); }
.rule-warning, .muted-label { flex-shrink: 0; font-size: 10px; color: var(--warning-strong); }
.muted-label { color: var(--secondary); }
.route-target { display: flex; align-items: center; gap: 7px; min-width: 0; color: var(--secondary); }
.route-target > svg { width: 15px; height: 15px; }
.route-target .input { margin: 0; background: var(--card); font-size: 12px; padding: 9px 7px; }
.route-target.incomplete .input { color: var(--warning-strong); border-color: color-mix(in srgb, var(--warning) 40%, var(--proxy-border)); }
.drag-handle, .expand-button { display: grid; place-items: center; border: none; background: transparent; border-radius: 5px; color: var(--secondary); padding: 0; width: 24px; height: 30px; cursor: pointer; }
.drag-handle { width: 18px; cursor: grab; touch-action: none; }
.drag-handle:hover, .expand-button:hover { background: var(--control); color: var(--label); }
.expand-button svg { width: 15px; height: 15px; transition: transform .18s; }
.expanded .expand-button svg { transform: rotate(90deg); }
.rule-disabled .service-icon, .rule-disabled .service-title strong { color: var(--secondary); }
.rule-disabled .service-icon { background: var(--control); border-color: var(--proxy-border); }
.rule-editor { padding: 2px 56px 20px; animation: proxy-expand .18s ease-out; }
.editor-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px 22px; padding-top: 18px; border-top: 1px solid var(--proxy-border); }
.editor-aside { align-self: end; padding-bottom: 5px; }
.editor-footer { display: flex; align-items: center; justify-content: space-between; margin-top: 20px; }
.list-footnote { margin: 13px 0 0; font-size: 11px; line-height: 1.7; color: var(--secondary); }
.tab-panel > .notice { margin: 16px 0; }
.add-panel { margin-bottom: 20px; padding: 20px; border: 1px solid var(--proxy-border); border-radius: 12px; background: var(--card); animation: proxy-expand .18s ease-out; }
.provider-add { display: flex; align-items: center; gap: 12px; margin-top: 16px; }
.provider-add .input { margin: 0; flex: 1; max-width: 400px; }
.provider-add .btn { flex-shrink: 0; }
.or-divider { font-size: 11px; color: var(--secondary); }
.help-disclosure { border-top: 1px solid var(--proxy-border); padding-top: 19px; margin-top: 28px; }
.help-disclosure > summary > svg:last-child { margin-left: 0; }
.help-body { max-width: 820px; }
.empty-state { display: flex; align-items: center; flex-direction: column; gap: 12px; padding: 48px 24px; border: 1px dashed var(--proxy-border); border-radius: 12px; text-align: center; }
.empty-state > svg { width: 32px; height: 32px; margin-bottom: 6px; color: var(--secondary); }
.empty-state h3 { margin: 0; font-size: 15px; font-weight: 500; }
.empty-state p { margin: 0 0 10px; font-size: 12px; color: var(--secondary); }
.page-loading { display: flex; align-items: center; justify-content: center; gap: 12px; flex: 1; color: var(--secondary); font-size: 13px; }
.is-dragging { opacity: .55; background: var(--control); }
.is-drop-target { box-shadow: inset 0 2px var(--accent); }
.card-dragging { user-select: none; }
.card-dragging :deep(*) { cursor: grabbing !important; }
@keyframes proxy-expand { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: translateY(0); } }
@media (max-width: 1100px) {
  .workspace-head { padding: 26px 24px 0; }
  .proxy-content { padding: 22px 24px 30px; }
  .status-strip { gap: 18px; padding: 17px 18px; }
  .status-primary { min-width: 160px; }
  .status-item { min-width: 64px; }
  .scope-tag { display: none; }
  .rule-summary { gap: 12px; }
  .route-columns { gap: 12px; padding-left: 46px; grid-template-columns: minmax(0, 1fr) minmax(190px, 30%) 82px; }
  .rule-identity { gap: 10px; }
  .service-icon { width: 32px; height: 34px; }
  .rule-editor { padding-left: 46px; padding-right: 30px; }
}
@media (max-width: 880px) {
  .status-strip { gap: 14px; }
  .status-primary { min-width: 140px; }
  .rule-summary { grid-template-columns: 18px minmax(0, 1fr) 44px 26px; gap: 12px; padding: 17px 16px; }
  .route-target { grid-column: 2; grid-row: 2; }
  .rule-summary > .switch { grid-column: 3; grid-row: 1; }
  .rule-summary > .expand-button { grid-column: 4; grid-row: 1; }
  .route-columns { display: none; }
  .rule-card:first-of-type { border-top: 0; }
  .route-target .input { max-width: 100%; }
  .page-top { gap: 14px; }
  .page-actions { gap: 6px; }
  .page-actions .btn { padding: 9px 12px; }
  .provider-add { flex-wrap: wrap; }
  .provider-add .input { flex-basis: 60%; max-width: none; }
}
@media (max-width: 600px) {
  .workspace-head { padding: 20px 16px 0; gap: 17px; }
  .proxy-content { padding: 20px 16px 28px; }
  .page-top { flex-wrap: wrap; }
  .page-actions { width: 100%; justify-content: flex-end; }
  .eyebrow { display: none; }
  .page-top :deep(h1) { font-size: 24px; }
  .status-strip { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); padding: 16px; gap: 18px; }
  .status-primary { grid-column: 1 / -1; grid-row: 1; min-width: 0; padding-right: 70px; }
  .status-item { min-width: 0; flex-direction: row; align-items: baseline; gap: 10px; }
  .status-item > span { white-space: nowrap; }
  .status-action { grid-column: 2; grid-row: 1; justify-self: end; }
  .workspace-tabs { gap: 26px; }
  .pending-banner { align-items: flex-start; }
  .pending-banner strong { display: block; }
  .section-title { gap: 12px; }
  .section-title .btn { padding: 8px 12px; white-space: nowrap; }
  .rule-editor { padding: 0 18px 18px; }
  .editor-fields { grid-template-columns: minmax(0, 1fr); }
  .editor-aside { margin-top: -5px; }
  .rule-summary { gap: 10px; padding: 17px 12px; }
  .service-title strong { font-size: 13px; }
  .add-panel { padding: 16px; }
  .provider-add .input { flex-basis: 100%; }
}
</style>
