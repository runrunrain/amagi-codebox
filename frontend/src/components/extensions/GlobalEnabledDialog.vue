<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { GetPluginDetail } from '../../../wailsjs/go/plugin/Service'
import { SetGlobalEnabled } from '../../../wailsjs/go/workspace/Service'
import { plugin, workspace } from '../../../wailsjs/go/models'
import { useToast } from '../../composables/useToast'

type Mode = 'all' | 'partial'
interface Draft { enabled: boolean; enabledAll: boolean; tools: string[]; selectedKeys: string[] }

const props = defineProps<{ installedPlugins: plugin.InstalledPlugin[]; entries: workspace.GlobalEnabled[] }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'saved', result: workspace.DeployResult): void }>()
const { showError } = useToast()

const loading = ref(false)
const saving = ref(false)
const selectedPluginId = ref('')
const details = ref<Record<string, plugin.PluginDetail>>({})
const drafts = ref<Record<string, Draft>>({})
const toolOptions = [
  { value: 'claude', label: 'Claude', desc: '当前实际部署目标' },
  { value: 'opencode', label: 'OpenCode', desc: '仅保留后端边界' },
  { value: 'cursor', label: 'Cursor', desc: '仅保留后端边界' },
  { value: 'vscode', label: 'VS Code', desc: '仅保留后端边界' },
]

const sortedPlugins = computed(() => [...props.installedPlugins].sort((a, b) => a.enabled === b.enabled ? a.name.localeCompare(b.name) : (a.enabled ? -1 : 1)))
const currentDetail = computed(() => details.value[selectedPluginId.value])
const currentDraft = computed(() => drafts.value[selectedPluginId.value])
const busy = computed(() => loading.value || saving.value)

const keyOf = (item: { type: string; name: string }) => `${item.type}:${item.name}`
const enabledSubItems = (detail?: plugin.PluginDetail) => (detail?.subItems || []).filter(item => item.enabled)
const pluginTypeLabel = (value = 'unknown') => ({ integration: '集成', hybrid: '混合', skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', unknown: '未知' } as Record<string, string>)[value] || value
const subItemTypeLabel = (value: string) => ({ skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', claude: 'Claude' } as Record<string, string>)[value] || value
const typeClass = (value?: string) => `type-${value || 'unknown'}`
const canUsePartial = (detail?: plugin.PluginDetail) => enabledSubItems(detail).length > 0
const getDraft = (id: string): Draft => drafts.value[id] || { enabled: false, enabledAll: true, tools: ['claude'], selectedKeys: [] }

function setDraft(id: string, draft: Draft) { drafts.value = { ...drafts.value, [id]: draft } }
function toggleEnabled(id: string) {
  const draft = getDraft(id)
  if (draft.enabled) return setDraft(id, { ...draft, enabled: false })
  setDraft(id, { enabled: true, enabledAll: true, tools: draft.tools.length ? draft.tools : ['claude'], selectedKeys: enabledSubItems(details.value[id]).map(item => keyOf(item)) })
}
function toggleTool(id: string, tool: string) {
  const set = new Set(getDraft(id).tools)
  if (set.has(tool)) set.delete(tool)
  else set.add(tool)
  setDraft(id, { ...getDraft(id), tools: [...set] })
}
function setMode(id: string, mode: Mode) {
  if (mode === 'partial' && !canUsePartial(details.value[id])) return
  const draft = getDraft(id)
  setDraft(id, { ...draft, enabled: true, enabledAll: mode === 'all', selectedKeys: mode === 'all' ? [] : (draft.selectedKeys.length ? draft.selectedKeys : enabledSubItems(details.value[id]).map(item => keyOf(item))) })
}
function toggleSubItem(id: string, item: plugin.SubItem) {
  const draft = getDraft(id)
  const key = keyOf(item)
  setDraft(id, { ...draft, enabled: true, enabledAll: false, selectedKeys: draft.selectedKeys.includes(key) ? draft.selectedKeys.filter(entry => entry !== key) : [...draft.selectedKeys, key] })
}
function subItemSelected(id: string, item: plugin.SubItem) { return getDraft(id).selectedKeys.includes(keyOf(item)) }
function statusText(id: string, pluginItem: plugin.InstalledPlugin) {
  const draft = getDraft(id)
  if (draft.enabled) return draft.enabledAll ? '整插件全局启用' : '局部全局启用'
  return pluginItem.enabled ? '未全局启用' : '插件已禁用'
}
async function loadDetails() {
  loading.value = true
  try {
    const pairs = await Promise.all(sortedPlugins.value.map(async item => [item.id, await GetPluginDetail(item.id)] as const))
    details.value = Object.fromEntries(pairs)
    const map = new Map(props.entries.map(item => [item.pluginId, item]))
    const next: Record<string, Draft> = {}
    for (const pluginItem of sortedPlugins.value) {
      const entry = map.get(pluginItem.id)
      next[pluginItem.id] = { enabled: Boolean(entry), enabledAll: entry ? entry.enabledAll : true, tools: entry ? [...entry.tools] : ['claude'], selectedKeys: entry ? (entry.enabledSubItems || []).map(ref => keyOf(ref)) : [] }
      if (entry && !entry.enabledAll && next[pluginItem.id].selectedKeys.length === 0) next[pluginItem.id].selectedKeys = enabledSubItems(details.value[pluginItem.id]).map(item => keyOf(item))
    }
    drafts.value = next
    if (!details.value[selectedPluginId.value]) selectedPluginId.value = sortedPlugins.value[0]?.id || ''
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
    if (!draft.enabledAll && draft.selectedKeys.length === 0) return showError(`插件 ${pluginItem.name} 的局部全局启用至少需要一个子项`)
  }
  saving.value = true
  try {
    const payload = sortedPlugins.value.flatMap(pluginItem => {
      const draft = getDraft(pluginItem.id)
      const detail = details.value[pluginItem.id]
      if (!draft.enabled || !detail) return []
      const enabledRefs = draft.enabledAll ? [] : enabledSubItems(detail).filter(item => draft.selectedKeys.includes(keyOf(item))).map(item => ({ type: item.type, name: item.name }))
      return [{ pluginId: pluginItem.id, enabledAll: draft.enabledAll, enabledSubItems: enabledRefs, tools: [...draft.tools], deployedAt: '' }]
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
          <div class="dialog-header"><div><h2 class="dialog-title">全局启用</h2><p class="dialog-hint">整插件全局启用会让插件从工作区选择页中消失；局部全局启用只会锁定对应子项。</p></div><button class="close-btn" @click="emit('close')" :disabled="busy">关闭</button></div>
          <div class="dialog-body" v-if="sortedPlugins.length">
            <div class="plugin-nav">
              <button v-for="pluginItem in sortedPlugins" :key="pluginItem.id" class="plugin-nav-item" :class="{ active: selectedPluginId === pluginItem.id }" @click="selectedPluginId = pluginItem.id">
                <div class="row-between"><span class="plugin-nav-name">{{ pluginItem.name }}</span><span class="state-dot" :class="{ active: getDraft(pluginItem.id).enabled }"></span></div>
                <p class="dialog-hint">{{ statusText(pluginItem.id, pluginItem) }}</p>
              </button>
            </div>
            <div class="plugin-detail" v-if="currentDetail && currentDraft">
              <div class="detail-card"><div class="detail-title-row"><h3 class="detail-title">{{ currentDetail.name }}</h3><span class="badge" :class="typeClass(currentDetail.pluginType)">{{ pluginTypeLabel(currentDetail.pluginType) }}</span><span class="badge" v-if="currentDetail.scope">{{ currentDetail.scope }}</span><span class="badge success" v-if="currentDetail.hasClaudeMd">CLAUDE.md</span></div><p class="dialog-hint">{{ currentDetail.manifest?.description || '无描述信息' }}</p></div>
              <div class="detail-card"><div class="row-between gap-start"><div><h4 class="detail-section-title">是否全局启用</h4><p class="dialog-hint">保存时会一次性写回所有全局启用配置。</p></div><button class="btn" :class="currentDraft.enabled ? 'primary' : 'secondary'" @click="toggleEnabled(selectedPluginId)">{{ currentDraft.enabled ? '已启用' : '未启用' }}</button></div></div>
              <div class="detail-card" v-if="currentDraft.enabled"><h4 class="detail-section-title">工具目标</h4><div class="tool-grid"><button v-for="tool in toolOptions" :key="tool.value" class="tool-pill" :class="{ active: currentDraft.tools.includes(tool.value) }" @click="toggleTool(selectedPluginId, tool.value)"><span>{{ tool.label }}</span><small>{{ tool.desc }}</small></button></div></div>
              <div class="detail-card" v-if="currentDraft.enabled"><div class="row-between gap-start"><div><h4 class="detail-section-title">启用范围</h4><p class="dialog-hint">整插件会隐藏工作区入口；局部启用会保留插件，但对应子项在工作区里会变成不可选状态。</p></div><div class="mode-row"><button class="mode-pill" :class="{ active: currentDraft.enabledAll }" @click="setMode(selectedPluginId, 'all')">整插件</button><button class="mode-pill" :class="{ active: !currentDraft.enabledAll }" :disabled="!canUsePartial(currentDetail)" @click="setMode(selectedPluginId, 'partial')">局部子项</button></div></div>
                <div class="subitem-list" v-if="!currentDraft.enabledAll">
                  <button v-for="subItem in enabledSubItems(currentDetail)" :key="keyOf(subItem)" class="subitem-row" :class="{ active: subItemSelected(selectedPluginId, subItem) }" @click="toggleSubItem(selectedPluginId, subItem)"><div><div class="subitem-name">{{ subItem.name }} <span class="badge">{{ subItemTypeLabel(subItem.type) }}</span></div><div class="dialog-hint">{{ subItem.path }}</div></div><span class="badge">{{ subItemSelected(selectedPluginId, subItem) ? '已选中' : '点击选择' }}</span></button>
                </div>
              </div>
            </div>
          </div>
          <div class="empty-state" v-else><p class="detail-title">当前没有已安装插件</p><p class="dialog-hint">请先安装插件，再配置全局启用。</p></div>
          <div class="dialog-footer"><button class="btn secondary" @click="emit('close')" :disabled="busy">取消</button><button class="btn primary" @click="saveAll" :disabled="busy">{{ saving ? '保存中...' : '保存全局配置' }}</button></div>
        </div>
      </div>
    </transition>
  </Teleport>
</template>

<style scoped>
.dialog-overlay,.dialog-header,.dialog-footer,.row-between,.mode-row,.tool-grid,.detail-title-row{display:flex}.dialog-overlay{position:fixed;inset:0;align-items:center;justify-content:center;background:rgba(15,18,25,.82);z-index:130;backdrop-filter:blur(4px)}.dialog-panel{width:min(1080px,calc(100vw - 40px));max-height:calc(100vh - 40px);display:flex;flex-direction:column;background:#1a1f2e;border:1px solid #2a2f3e;border-radius:10px;box-shadow:0 12px 48px rgba(0,0,0,.42);overflow:hidden}.dialog-header,.dialog-footer{justify-content:space-between;align-items:center;gap:12px;padding:18px 20px;border-bottom:1px solid #2a2f3e}.dialog-footer{border-bottom:none;border-top:1px solid #2a2f3e}.dialog-title,.detail-title,.detail-section-title{margin:0;color:#e0e6ed}.dialog-title{font-size:18px;font-weight:600}.dialog-hint{margin:0;color:#8899aa;font-size:13px;line-height:1.5}.close-btn,.btn,.tool-pill,.mode-pill,.plugin-nav-item,.subitem-row{font-family:inherit}.close-btn,.btn,.mode-pill{border:1px solid #2a2f3e;background:transparent;color:#e0e6ed;border-radius:6px;cursor:pointer;transition:.15s}.close-btn{padding:8px 12px}.dialog-body{display:grid;grid-template-columns:280px minmax(0,1fr);flex:1;min-height:0}.plugin-nav{padding:16px;display:flex;flex-direction:column;gap:10px;background:rgba(15,18,25,.2);border-right:1px solid #2a2f3e;overflow:auto}.plugin-nav-item{padding:12px 14px;border:1px solid #2a2f3e;border-radius:8px;background:#0f1219;text-align:left;color:inherit;cursor:pointer}.plugin-nav-item.active,.mode-pill.active,.tool-pill.active,.subitem-row.active{border-color:#4fc3f7;background:rgba(79,195,247,.08)}.plugin-nav-item:hover,.tool-pill:hover,.subitem-row:hover,.close-btn:hover,.btn:hover:not(:disabled){border-color:#3a4f5e}.plugin-nav-name,.subitem-name{color:#e0e6ed;font-weight:600}.state-dot{width:8px;height:8px;border-radius:999px;background:#5a6a7a}.state-dot.active{background:#66bb6a;box-shadow:0 0 8px rgba(102,187,106,.35)}.plugin-detail{padding:20px;display:flex;flex-direction:column;gap:16px;overflow:auto}.detail-card{display:flex;flex-direction:column;gap:12px;padding:16px;border:1px solid #2a2f3e;border-radius:8px;background:#0f1219}.detail-title-row,.row-between,.mode-row,.tool-grid{gap:10px;align-items:center}.row-between{justify-content:space-between}.gap-start{align-items:flex-start}.tool-grid{flex-wrap:wrap}.tool-pill{min-width:140px;padding:10px 12px;display:flex;flex-direction:column;align-items:flex-start;color:#8899aa}.tool-pill small{font-size:12px;line-height:1.4}.badge{display:inline-flex;align-items:center;justify-content:center;padding:4px 8px;border-radius:999px;font-size:12px;font-weight:600;background:rgba(136,153,170,.12);color:#aab8c5}.badge.success{background:rgba(102,187,106,.12);color:#66bb6a}.type-integration{background:rgba(79,195,247,.12);color:#4fc3f7}.type-hybrid{background:rgba(255,167,38,.12);color:#ffa726}.type-skill,.type-hook,.type-command,.type-agent,.type-mcp,.type-unknown{background:rgba(136,153,170,.12);color:#ccd6e0}.mode-pill{padding:6px 12px;border-radius:999px;color:#8899aa}.mode-pill:disabled,.btn:disabled{opacity:.5;cursor:not-allowed}.subitem-list{display:flex;flex-direction:column;gap:10px}.subitem-row{width:100%;padding:12px 14px;display:flex;align-items:center;justify-content:space-between;gap:14px;border:1px solid #2a2f3e;border-radius:8px;background:#141925;color:inherit;text-align:left}.empty-state{padding:32px 20px;text-align:center;display:flex;flex-direction:column;gap:8px}.btn{display:inline-flex;align-items:center;justify-content:center;padding:8px 16px;white-space:nowrap}.btn.primary{background:#4fc3f7;border-color:#4fc3f7;color:#0f1219}.btn.primary:hover:not(:disabled){background:#7bd4f9;border-color:#7bd4f9}.btn.secondary:hover:not(:disabled){color:#4fc3f7}.dialog-fade-enter-active,.dialog-fade-leave-active{transition:opacity .15s ease}.dialog-fade-enter-from,.dialog-fade-leave-to{opacity:0}@media (max-width:960px){.dialog-body{grid-template-columns:1fr}.plugin-nav{border-right:none;border-bottom:1px solid #2a2f3e;max-height:240px}}@media (max-width:720px){.dialog-panel{width:calc(100vw - 20px);max-height:calc(100vh - 20px)}.dialog-header,.dialog-footer,.row-between,.subitem-row{flex-direction:column;align-items:stretch}.dialog-footer>*{width:100%}}
</style>
