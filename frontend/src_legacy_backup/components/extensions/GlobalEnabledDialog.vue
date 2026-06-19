<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { GetPluginDetail } from '../../../wailsjs/go/plugin/Service'
import { SetGlobalEnabled } from '../../../wailsjs/go/workspace/Service'
import { plugin, workspace } from '../../../wailsjs/go/models'
import { useToast } from '../../composables/useToast'

type Mode = 'all' | 'partial'
interface Draft { enabled: boolean; enabledAll: boolean; tools: string[]; selectedKeys: string[] }
interface DetailGroup { type: string; label: string; items: plugin.SubItem[] }

const props = defineProps<{ installedPlugins: plugin.InstalledPlugin[]; entries: workspace.GlobalEnabled[] }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'saved', result: workspace.DeployResult): void }>()
const { showError } = useToast()

const loading = ref(false)
const saving = ref(false)
const selectedPluginId = ref('')
const details = ref<Record<string, plugin.PluginDetail>>({})
const loadErrors = ref<Record<string, string>>({})
const drafts = ref<Record<string, Draft>>({})
const expandedGroups = ref<Record<string, boolean>>({})

const toolOptions = [
  { value: 'claude', label: 'Claude', desc: '当前实际部署目标' },
  { value: 'opencode', label: 'OpenCode', desc: '仅保留后端边界' },
  { value: 'cursor', label: 'Cursor', desc: '仅保留后端边界' },
  { value: 'vscode', label: 'VS Code', desc: '仅保留后端边界' },
]

const sortedPlugins = computed(() => [...props.installedPlugins].sort((a, b) => a.enabled === b.enabled ? a.name.localeCompare(b.name) : (a.enabled ? -1 : 1)))
const entryMap = computed(() => new Map(props.entries.map(item => [item.pluginId, item] as const)))
const currentPlugin = computed(() => sortedPlugins.value.find(item => item.id === selectedPluginId.value) || null)
const currentDetail = computed(() => details.value[selectedPluginId.value])
const currentDraft = computed(() => drafts.value[selectedPluginId.value])
const currentLoadError = computed(() => loadErrors.value[selectedPluginId.value] || '')
const busy = computed(() => loading.value || saving.value)

const keyOf = (item: { type: string; name: string }) => `${item.type}:${item.name}`
const groupKey = (pluginId: string, type: string) => `${pluginId}:${type}`
const pluginTypeLabel = (value = 'unknown') => ({ integration: '集成', hybrid: '混合', skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', unknown: '未知' } as Record<string, string>)[value] || value
const subItemTypeLabel = (value: string) => ({ skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', claude: 'Claude' } as Record<string, string>)[value] || value
const typeClass = (value?: string) => `type-${value || 'unknown'}`
const enabledSubItems = (detail?: plugin.PluginDetail) => (detail?.subItems || []).filter(item => item.enabled)
const canUsePartial = (detail?: plugin.PluginDetail) => enabledSubItems(detail).length > 0
const getDraft = (id: string): Draft => drafts.value[id] || { enabled: false, enabledAll: true, tools: ['claude'], selectedKeys: [] }
const subItemSelected = (id: string, item: plugin.SubItem) => getDraft(id).selectedKeys.includes(keyOf(item))
const isGroupExpanded = (pluginId: string, type: string) => expandedGroups.value[groupKey(pluginId, type)] ?? true

function normalizeError(error: unknown) {
  if (error instanceof Error) return error.message
  return String(error)
}

function setDraft(id: string, draft: Draft) {
  drafts.value = { ...drafts.value, [id]: draft }
}

function groupedSubItems(detail?: plugin.PluginDetail): DetailGroup[] {
  const groups: DetailGroup[] = []
  const lookup = new Map<string, DetailGroup>()
  for (const item of enabledSubItems(detail)) {
    let group = lookup.get(item.type)
    if (!group) {
      group = { type: item.type, label: subItemTypeLabel(item.type), items: [] }
      lookup.set(item.type, group)
      groups.push(group)
    }
    group.items.push(item)
  }
  return groups
}

function toggleGroup(pluginId: string, type: string) {
  const key = groupKey(pluginId, type)
  expandedGroups.value[key] = !isGroupExpanded(pluginId, type)
}

function toggleEnabled(id: string) {
  const draft = getDraft(id)
  if (draft.enabled) return setDraft(id, { ...draft, enabled: false })
  setDraft(id, {
    enabled: true,
    enabledAll: true,
    tools: draft.tools.length ? draft.tools : ['claude'],
    selectedKeys: enabledSubItems(details.value[id]).map(item => keyOf(item)),
  })
}

function toggleTool(id: string, tool: string) {
  const next = new Set(getDraft(id).tools)
  if (next.has(tool)) next.delete(tool)
  else next.add(tool)
  setDraft(id, { ...getDraft(id), tools: [...next] })
}
function setMode(id: string, mode: Mode) {
  if (mode === 'partial' && !canUsePartial(details.value[id])) return
  const draft = getDraft(id)
  setDraft(id, {
    ...draft,
    enabled: true,
    enabledAll: mode === 'all',
    selectedKeys: mode === 'all' ? [] : (draft.selectedKeys.length ? draft.selectedKeys : enabledSubItems(details.value[id]).map(item => keyOf(item))),
  })
}

function toggleSubItem(id: string, item: plugin.SubItem) {
  const draft = getDraft(id)
  const key = keyOf(item)
  setDraft(id, {
    ...draft,
    enabled: true,
    enabledAll: false,
    selectedKeys: draft.selectedKeys.includes(key) ? draft.selectedKeys.filter(entry => entry !== key) : [...draft.selectedKeys, key],
  })
}

function statusText(id: string, pluginItem: plugin.InstalledPlugin) {
  const draft = getDraft(id)
  if (loadErrors.value[id]) return draft.enabled ? '详情异常，修复后才能保存' : '详情加载失败'
  if (draft.enabled) return draft.enabledAll ? '整插件全局启用' : '局部全局启用'
  return pluginItem.enabled ? '未全局启用' : '插件已禁用'
}

async function loadDetails() {
  loading.value = true
  try {
    const results = await Promise.allSettled(sortedPlugins.value.map(item => GetPluginDetail(item.id)))
    const nextDetails: Record<string, plugin.PluginDetail> = {}
    const nextErrors: Record<string, string> = {}

    sortedPlugins.value.forEach((item, index) => {
      const result = results[index]
      if (result.status === 'fulfilled') nextDetails[item.id] = result.value
      else nextErrors[item.id] = normalizeError(result.reason)
    })

    details.value = nextDetails
    loadErrors.value = nextErrors

    const nextDrafts: Record<string, Draft> = {}
    for (const pluginItem of sortedPlugins.value) {
      const entry = entryMap.value.get(pluginItem.id)
      const detail = nextDetails[pluginItem.id]
      const selectedKeys = entry ? (entry.enabledSubItems || []).map(ref => keyOf(ref)) : []
      if (entry && !entry.enabledAll && selectedKeys.length === 0) {
        selectedKeys.push(...enabledSubItems(detail).map(item => keyOf(item)))
      }
      nextDrafts[pluginItem.id] = {
        enabled: Boolean(entry),
        enabledAll: entry ? entry.enabledAll : true,
        tools: entry && entry.tools.length ? [...entry.tools] : ['claude'],
        selectedKeys,
      }
    }
    drafts.value = nextDrafts

    if (!sortedPlugins.value.length) selectedPluginId.value = ''
    else if (!selectedPluginId.value || !sortedPlugins.value.some(item => item.id === selectedPluginId.value)) selectedPluginId.value = sortedPlugins.value[0].id
  } catch (error) {
    showError(`加载全局启用详情失败: ${error}`)
  } finally {
    loading.value = false
  }
}

async function saveAll() {
  for (const pluginItem of sortedPlugins.value) {
    const draft = getDraft(pluginItem.id)
    if (!draft.enabled) continue
    if (draft.tools.length === 0) return showError(`插件 ${pluginItem.name} 尚未选择部署工具`)

    const detail = details.value[pluginItem.id]
    if (!detail) {
      return showError(`插件 ${pluginItem.name} 详情加载失败，请先修复插件后再保存全局配置`)
    }
    if (!draft.enabledAll && draft.selectedKeys.length === 0) {
      return showError(`插件 ${pluginItem.name} 的局部全局启用至少需要一个子项`)
    }
  }
  saving.value = true
  try {
    const payload = sortedPlugins.value
      .flatMap(pluginItem => {
        const draft = getDraft(pluginItem.id)
        const detail = details.value[pluginItem.id]
        const existing = entryMap.value.get(pluginItem.id)
        if (!draft.enabled || !detail) return []

        const enabledRefs = draft.enabledAll
          ? []
          : enabledSubItems(detail)
              .filter(item => draft.selectedKeys.includes(keyOf(item)))
              .map(item => plugin.SubItemRef.createFrom({ type: item.type, name: item.name }))

        return [workspace.GlobalEnabled.createFrom({ pluginId: pluginItem.id, enabledAll: draft.enabledAll, enabledSubItems: enabledRefs, tools: [...draft.tools], deployedAt: existing?.deployedAt || '' })]
      })
    emit('saved', await SetGlobalEnabled(payload))
  } catch (error) {
    showError(`保存全局启用失败: ${error}`)
  } finally {
    saving.value = false
  }
}

watch(() => [props.installedPlugins, props.entries], () => { void loadDetails() }, { immediate: true })
</script>

<template>
  <Teleport to="body">
    <transition name="dialog-fade">
      <div class="dialog-overlay">
        <div class="dialog-panel" @click.stop>
          <div class="dialog-header">
            <div>
              <h2 class="dialog-title">全局启用</h2>
              <p class="dialog-hint">整插件全局启用会让插件从工作区选择页中消失；局部全局启用只会锁定对应子项。</p>
            </div>
            <button class="close-btn" @click="emit('close')" :disabled="busy">关闭</button>
          </div>

          <div class="dialog-body" v-if="sortedPlugins.length">
            <div class="plugin-nav">
              <button v-for="pluginItem in sortedPlugins" :key="pluginItem.id" class="plugin-nav-item" :class="{ active: selectedPluginId === pluginItem.id, warning: !!loadErrors[pluginItem.id] }" @click="selectedPluginId = pluginItem.id">
                <div class="row-between">
                  <span class="plugin-nav-name">{{ pluginItem.name }}</span>
                  <span class="state-dot" :class="{ active: getDraft(pluginItem.id).enabled }"></span>
                </div>
                <p class="dialog-hint">{{ statusText(pluginItem.id, pluginItem) }}</p>
              </button>
            </div>

            <div class="plugin-detail" v-if="currentPlugin && currentDraft">
              <div class="detail-card">
                <div class="detail-title-row">
                  <h3 class="detail-title">{{ currentDetail?.name || currentPlugin.name }}</h3>
                  <span v-if="currentDetail" class="badge" :class="typeClass(currentDetail.pluginType)">{{ pluginTypeLabel(currentDetail.pluginType) }}</span>
                  <span class="badge" v-if="currentDetail?.scope || currentPlugin.scope">{{ currentDetail?.scope || currentPlugin.scope }}</span>
                  <span class="badge success" v-if="currentDetail?.hasClaudeMd">CLAUDE.md</span>
                  <span class="badge warning" v-if="currentLoadError">详情异常</span>
                </div>
                <p class="dialog-hint">{{ currentDetail?.manifest?.description || '无描述信息' }}</p>
              </div>

              <div class="detail-card" v-if="currentLoadError">
                <h4 class="detail-section-title">插件详情不可用</h4>
                <p class="dialog-hint">该插件的 manifest 或子项读取失败。当前弹窗不会再因为单个坏插件而整体失败；但在插件修复之前，不能保存这条全局启用配置。</p>
                <pre class="error-block">{{ currentLoadError }}</pre>
              </div>

              <div class="detail-card">
                <div class="row-between gap-start">
                  <div>
                    <h4 class="detail-section-title">是否全局启用</h4>
                    <p class="dialog-hint">保存时会一次性写回所有全局启用配置。</p>
                  </div>
                  <button class="btn" :class="currentDraft.enabled ? 'primary' : 'secondary'" @click="toggleEnabled(selectedPluginId)">{{ currentDraft.enabled ? '已启用' : '未启用' }}</button>
                </div>
              </div>
              <div class="detail-card" v-if="currentDraft.enabled">
                <h4 class="detail-section-title">工具目标</h4>
                <div class="tool-grid">
                  <button v-for="tool in toolOptions" :key="tool.value" class="tool-pill" :class="{ active: currentDraft.tools.includes(tool.value) }" @click="toggleTool(selectedPluginId, tool.value)">
                    <span>{{ tool.label }}</span>
                    <small>{{ tool.desc }}</small>
                  </button>
                </div>
              </div>

              <div class="detail-card" v-if="currentDraft.enabled">
                <div class="row-between gap-start">
                  <div>
                    <h4 class="detail-section-title">启用范围</h4>
                    <p class="dialog-hint">整插件会隐藏工作区入口；局部启用会保留插件，但对应子项在工作区里会变成不可选状态。</p>
                  </div>
                  <div class="mode-row">
                    <button class="mode-pill" :class="{ active: currentDraft.enabledAll }" @click="setMode(selectedPluginId, 'all')">整插件</button>
                    <button class="mode-pill" :class="{ active: !currentDraft.enabledAll }" :disabled="!canUsePartial(currentDetail)" @click="setMode(selectedPluginId, 'partial')">局部子项</button>
                  </div>
                </div>

                <template v-if="!currentDraft.enabledAll && currentDetail">
                  <div class="subitem-groups" v-if="groupedSubItems(currentDetail).length">
                    <div class="subitem-group" v-for="group in groupedSubItems(currentDetail)" :key="group.type">
                      <button type="button" class="subitem-group-header" @click="toggleGroup(selectedPluginId, group.type)">
                        <span class="subitem-group-meta">
                          <span class="subitem-group-title">{{ group.label }}</span>
                          <span class="badge count-badge">{{ group.items.length }}</span>
                        </span>
                        <svg :class="['chevron', { expanded: isGroupExpanded(selectedPluginId, group.type) }]" viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                      </button>
                      <div class="subitem-list" v-if="isGroupExpanded(selectedPluginId, group.type)">
                        <div class="subitem-row" v-for="subItem in group.items" :key="keyOf(subItem)">
                          <div class="subitem-copy">
                            <div class="subitem-name-row">
                              <span class="subitem-name">{{ subItem.name }}</span>
                              <span class="badge">{{ subItemTypeLabel(subItem.type) }}</span>
                            </div>
                            <div class="dialog-hint">{{ subItem.path }}</div>
                          </div>
                          <div class="subitem-actions">
                            <span class="toggle-label" :class="{ 'text-enabled': subItemSelected(selectedPluginId, subItem), 'text-disabled': !subItemSelected(selectedPluginId, subItem) }">{{ subItemSelected(selectedPluginId, subItem) ? '已启用' : '未启用' }}</span>
                            <button class="ios-toggle" :class="{ active: subItemSelected(selectedPluginId, subItem) }" @click="toggleSubItem(selectedPluginId, subItem)"></button>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                  <p v-else class="dialog-hint">当前没有可用于局部全局启用的已启用子项。</p>
                </template>

                <div v-else-if="!currentDraft.enabledAll && currentLoadError" class="warning-box">
                  <p class="dialog-hint">当前无法读取插件子项，因此不能编辑或保存局部全局启用；请先修复插件目录后再保存。</p>
                </div>
              </div>
            </div>
          </div>

          <div class="empty-state" v-else>
            <p class="detail-title">当前没有已安装插件</p>
            <p class="dialog-hint">请先安装插件，再配置全局启用。</p>
          </div>

          <div class="dialog-footer">
            <button class="btn secondary" @click="emit('close')" :disabled="busy">取消</button>
            <button class="btn primary" @click="saveAll" :disabled="busy">{{ saving ? '保存中...' : '保存全局配置' }}</button>
          </div>
        </div>
      </div>
    </transition>
  </Teleport>
</template>

<style scoped>
.dialog-overlay { position: fixed; inset: 0; display: flex; align-items: center; justify-content: center; background: rgba(15, 18, 25, 0.82); z-index: 130; backdrop-filter: blur(4px); }
.dialog-panel { width: min(1080px, calc(100vw - 40px)); max-height: calc(100vh - 40px); display: flex; flex-direction: column; background: #1a1f2e; border: 1px solid #2a2f3e; border-radius: 10px; box-shadow: 0 12px 48px rgba(0, 0, 0, 0.42); overflow: hidden; }
.dialog-header, .dialog-footer, .row-between, .mode-row, .tool-grid, .detail-title-row { display: flex; }
.dialog-header, .dialog-footer { justify-content: space-between; align-items: center; gap: 12px; padding: 18px 20px; border-bottom: 1px solid #2a2f3e; }
.dialog-footer { border-bottom: none; border-top: 1px solid #2a2f3e; }
.dialog-title, .detail-title, .detail-section-title { margin: 0; color: #e0e6ed; }
.dialog-title { font-size: 18px; font-weight: 600; }
.dialog-hint { margin: 0; color: #8899aa; font-size: 13px; line-height: 1.5; }
.close-btn, .btn, .tool-pill, .mode-pill, .plugin-nav-item, .subitem-group-header { font-family: inherit; }
.close-btn, .btn, .mode-pill { border: 1px solid #2a2f3e; background: transparent; color: #e0e6ed; border-radius: 6px; cursor: pointer; transition: 0.15s; }
.close-btn { padding: 8px 12px; }
.dialog-body { display: grid; grid-template-columns: 280px minmax(0, 1fr); flex: 1; min-height: 0; }
.plugin-nav { padding: 16px; display: flex; flex-direction: column; gap: 10px; background: rgba(15, 18, 25, 0.2); border-right: 1px solid #2a2f3e; overflow: auto; }
.plugin-nav-item { padding: 12px 14px; border: 1px solid #2a2f3e; border-radius: 8px; background: #0f1219; text-align: left; color: inherit; cursor: pointer; }
.plugin-nav-item.active, .mode-pill.active, .tool-pill.active { border-color: #4fc3f7; background: rgba(79, 195, 247, 0.08); }
.plugin-nav-item.warning { border-color: rgba(255, 167, 38, 0.3); }
.plugin-nav-item:hover, .tool-pill:hover, .close-btn:hover, .btn:hover:not(:disabled), .subitem-group-header:hover { border-color: #3a4f5e; }
.plugin-nav-name, .subitem-name { color: #e0e6ed; font-weight: 600; }
.state-dot { width: 8px; height: 8px; border-radius: 999px; background: #5a6a7a; }
.state-dot.active { background: #66bb6a; box-shadow: 0 0 8px rgba(102, 187, 106, 0.35); }
.plugin-detail { padding: 20px; overflow: auto; display: flex; flex-direction: column; gap: 16px; }
.detail-card, .warning-box { background: #121622; border: 1px solid #2a2f3e; border-radius: 10px; padding: 16px; display: flex; flex-direction: column; gap: 14px; }
.warning-box { background: rgba(255, 167, 38, 0.05); border-color: rgba(255, 167, 38, 0.18); }
.detail-title-row, .row-between { justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap; }
.gap-start { align-items: flex-start; }
.badge { display: inline-flex; align-items: center; justify-content: center; padding: 3px 8px; font-size: 11px; border-radius: 999px; color: #ccd6e0; background: rgba(136, 153, 170, 0.12); border: 1px solid rgba(136, 153, 170, 0.15); }
.badge.success { color: #66bb6a; background: rgba(102, 187, 106, 0.1); border-color: rgba(102, 187, 106, 0.18); }
.badge.warning { color: #ffa726; background: rgba(255, 167, 38, 0.1); border-color: rgba(255, 167, 38, 0.18); }
.type-integration { color: #4fc3f7; background: rgba(79, 195, 247, 0.12); }
.type-hybrid { color: #ffa726; background: rgba(255, 167, 38, 0.12); }
.type-skill, .type-hook, .type-command, .type-agent, .type-mcp, .type-unknown { color: #ccd6e0; background: rgba(136, 153, 170, 0.12); }
.tool-grid { gap: 10px; flex-wrap: wrap; }
.tool-pill { min-width: 140px; display: flex; flex-direction: column; align-items: flex-start; gap: 4px; padding: 10px 12px; background: #0f1219; border: 1px solid #2a2f3e; border-radius: 8px; color: inherit; cursor: pointer; }
.tool-pill small { color: #8899aa; }
.mode-row { gap: 8px; flex-wrap: wrap; }
.mode-pill { padding: 8px 12px; }
.subitem-groups { display: flex; flex-direction: column; gap: 12px; }
.subitem-group { border: 1px solid #2a2f3e; border-radius: 8px; overflow: hidden; background: #0f1219; }
.subitem-group-header { width: 100%; display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 11px 14px; background: rgba(42, 47, 62, 0.32); border: none; color: #ccd6e0; cursor: pointer; text-align: left; }
.subitem-group-meta { display: flex; align-items: center; gap: 8px; }
.subitem-group-title { font-size: 13px; font-weight: 600; color: #e0e6ed; }
.count-badge { background: rgba(136, 153, 170, 0.12); color: #8899aa; font-size: 11px; }
.subitem-list { display: flex; flex-direction: column; }
.subitem-row { display: flex; justify-content: space-between; align-items: center; gap: 16px; padding: 12px 14px; border-top: 1px solid #2a2f3e; }
.subitem-copy { min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.subitem-name-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.subitem-actions { display: flex; align-items: center; gap: 10px; flex-shrink: 0; }
.toggle-label { font-size: 13px; font-weight: 600; white-space: nowrap; }
.text-enabled { color: #66bb6a; }
.text-disabled { color: #8899aa; }
.ios-toggle { position: relative; width: 44px; height: 24px; background: #2a2f3e; border-radius: 24px; cursor: pointer; transition: background 0.2s ease; border: none; outline: none; flex-shrink: 0; }
.ios-toggle.active { background: #66bb6a; }
.ios-toggle::after { content: ''; position: absolute; top: 2px; left: 2px; width: 20px; height: 20px; background: #fff; border-radius: 50%; transition: transform 0.2s cubic-bezier(0.25, 0.8, 0.25, 1), background 0.2s; box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2); }
.ios-toggle.active::after { transform: translateX(20px); }
.chevron { transition: transform 0.18s ease; }
.chevron.expanded { transform: rotate(180deg); }
.btn { padding: 8px 16px; border-radius: 6px; font-size: 14px; font-weight: 600; }
.btn.primary { background: #4fc3f7; color: #0f1219; }
.btn.primary:hover:not(:disabled) { background: #7bd4f9; }
.btn.secondary { background: transparent; color: #e0e6ed; }
.btn:disabled, .mode-pill:disabled, .close-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.error-block { margin: 0; padding: 12px; background: #0f1219; border: 1px solid rgba(239, 83, 80, 0.2); border-radius: 8px; color: #ef9a9a; font-size: 12px; white-space: pre-wrap; word-break: break-word; }
.empty-state { padding: 48px 24px; text-align: center; }
@media (max-width: 960px) { .dialog-panel { width: min(100vw - 20px, 1000px); max-height: calc(100vh - 20px); } .dialog-body { grid-template-columns: 1fr; } .plugin-nav { border-right: none; border-bottom: 1px solid #2a2f3e; max-height: 220px; } }
</style>
