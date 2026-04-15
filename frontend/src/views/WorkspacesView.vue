<script lang="ts" setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { BrowseDirectory } from '../../wailsjs/go/main/App'
import { GetInstalledPlugins } from '../../wailsjs/go/plugin/Service'
import { BuildScaffold, CleanWorkspace, CreateWorkspace, DeleteWorkspace, GetAvailablePluginsForWorkspace, GetGlobalEnabled, ListWorkspaces, SetWorkspacePlugins, SyncWorkspace, UpdateWorkspace } from '../../wailsjs/go/workspace/Service'
import { plugin, workspace } from '../../wailsjs/go/models'
import { useToast } from '../composables/useToast'
import GlobalEnabledDialog from '../components/extensions/GlobalEnabledDialog.vue'

type Mode = 'all' | 'partial'
type ActionKind = 'build' | 'sync' | 'clean' | 'global'
type ActionResult = workspace.DeployResult | workspace.CleanResult
interface Draft { selected: boolean; mode: Mode; selectedKeys: string[] }
interface ActionState { kind: ActionKind; label: string; result: ActionResult }
interface SubItemGroup { type: string; label: string; items: plugin.SubItem[] }

const route = useRoute()
const { showSuccess, showError, showInfo } = useToast()
const toolOptions = [
  { value: 'claude', label: 'Claude', desc: '当前唯一实际部署目标' },
  { value: 'opencode', label: 'OpenCode', desc: '执行时会返回后端警告' },
  { value: 'cursor', label: 'Cursor', desc: '执行时会返回后端警告' },
  { value: 'vscode', label: 'VS Code', desc: '执行时会返回后端警告' },
]

const loading = ref(false)
const savingWorkspace = ref(false)
const savingSelection = ref(false)
const actionLoading = ref(false)
const showGlobalDialog = ref(false)
const showDeleteDialog = ref(false)
const expandedSubItemGroups = ref<Record<string, boolean>>({})
const workspaces = ref<workspace.Workspace[]>([])
const installedPlugins = ref<plugin.InstalledPlugin[]>([])
const globalEntries = ref<workspace.GlobalEnabled[]>([])
const availablePlugins = ref<workspace.AvailablePlugin[]>([])
const selectedWorkspaceId = ref('')
const drafts = ref<Record<string, Draft>>({})
const lastAction = ref<ActionState | null>(null)
const form = reactive({ name: '', path: '', tools: ['claude'] as string[] })

const busy = computed(() => loading.value || savingWorkspace.value || savingSelection.value || actionLoading.value)
const selectedWorkspace = computed(() => workspaces.value.find(item => item.id === selectedWorkspaceId.value) || null)
const isCreateMode = computed(() => !selectedWorkspaceId.value)
const selectedCount = computed(() => Object.values(drafts.value).filter(item => item.selected).length)
const canSaveWorkspace = computed(() => form.path.trim() && form.tools.length > 0)
const canSaveSelection = computed(() => selectedWorkspaceId.value && availablePlugins.value.every(item => {
  const draft = drafts.value[item.id]
  return !draft?.selected || draft.mode === 'all' || draft.selectedKeys.length > 0
}))

const normalizePath = (value: string) => value.split('\\').join('/').replace(/\/+$/, '').trim().toLowerCase()
const basename = (value: string) => {
  const normalized = value.split('\\').join('/').replace(/\/+$/, '')
  const parts = normalized.split('/')
  return parts[parts.length - 1] || normalized
}
const keyOf = (item: { type: string; name: string }) => `${item.type}:${item.name}`
const enabledSubItems = (detail: { subItems?: plugin.SubItem[] }) => (detail.subItems || []).filter(item => item.enabled)
const hasGlobalSubItems = (detail: { subItems?: plugin.SubItem[] }) => (detail.subItems || []).some(item => item.globallyEnabled)
const canUseWhole = (detail: { subItems?: plugin.SubItem[] }) => !hasGlobalSubItems(detail)
const canUsePartial = (detail: { subItems?: plugin.SubItem[] }) => enabledSubItems(detail).some(item => item.selectable)
const canSelectPlugin = (detail: { subItems?: plugin.SubItem[] }) => canUseWhole(detail) || canUsePartial(detail)
const pluginTypeLabel = (value = 'unknown') => ({ integration: '集成', hybrid: '混合', skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', unknown: '未知' } as Record<string, string>)[value] || value
const subItemTypeLabel = (value: string) => ({ skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', claude: 'Claude' } as Record<string, string>)[value] || value
const toolLabel = (value: string) => toolOptions.find(item => item.value === value)?.label || value
const typeClass = (value?: string) => `type-${value || 'unknown'}`
const getDraft = (pluginId: string): Draft => drafts.value[pluginId] || { selected: false, mode: 'all', selectedKeys: [] }
const defaultPartialKeys = (detail: { subItems?: plugin.SubItem[] }) => enabledSubItems(detail).filter(item => item.selectable).map(item => keyOf(item))
const normalizePartialKeys = (detail: { subItems?: plugin.SubItem[] }, keys: string[]) => {
  const allowed = new Set(defaultPartialKeys(detail))
  const filtered = keys.filter(key => allowed.has(key))
  return filtered.length ? filtered : defaultPartialKeys(detail)
}
const subItemTypeOrder = ['skill', 'agent', 'command', 'hook', 'mcp', 'claude']
const subItemGroupKey = (pluginId: string, type: string) => `${pluginId}:${type}`
const isSubItemGroupExpanded = (pluginId: string, type: string) => expandedSubItemGroups.value[subItemGroupKey(pluginId, type)] ?? true

function resetForm(path = '') {
  form.path = path
  form.name = path ? basename(path) : ''
  form.tools = ['claude']
  drafts.value = {}
  availablePlugins.value = []
}
function fillForm(item: workspace.Workspace) {
  form.name = item.name || basename(item.path)
  form.path = item.path
  form.tools = [...(item.tools || ['claude'])]
}
async function browseWorkspaceDirectory() {
  try {
    const dir = await BrowseDirectory()
    if (dir) {
      form.path = dir
      if (!form.name.trim()) form.name = basename(dir)
    }
  } catch (error) {
    showError(`选择目录失败: ${error}`)
  }
}
async function selectWorkspace(id: string) {
  const current = workspaces.value.find(item => item.id === id)
  if (!current) return
  selectedWorkspaceId.value = id
  fillForm(current)
  try {
    availablePlugins.value = await GetAvailablePluginsForWorkspace(id)
    const map: Record<string, Draft> = {}
    const selectedMap = new Map(current.plugins.map(item => [item.pluginId, item]))
    for (const item of availablePlugins.value) {
      const existing = selectedMap.get(item.id)
      const keys = (existing?.enabledSubItems || []).map(ref => keyOf(ref))
      const allMode = keys.length === 0 && canUseWhole(item)
      map[item.id] = { selected: Boolean(existing), mode: allMode ? 'all' : 'partial', selectedKeys: normalizePartialKeys(item, keys) }
    }
    drafts.value = map
  } catch (error) {
    drafts.value = {}
    availablePlugins.value = []
    showError(`加载工作区插件失败: ${error}`)
  }
}

function applyRouteSelection() {
  const queryId = typeof route.query.workspaceId === 'string' ? route.query.workspaceId : ''
  const queryPath = typeof route.query.path === 'string' ? route.query.path : ''
  if (queryId) {
    const matched = workspaces.value.find(item => item.id === queryId)
    if (matched) return void selectWorkspace(matched.id)
  }
  if (queryPath) {
    const matched = workspaces.value.find(item => normalizePath(item.path) === normalizePath(queryPath))
    if (matched) return void selectWorkspace(matched.id)
    selectedWorkspaceId.value = ''
    return resetForm(queryPath)
  }
  if (selectedWorkspaceId.value) {
    const matched = workspaces.value.find(item => item.id === selectedWorkspaceId.value)
    if (matched) return void selectWorkspace(matched.id)
  }
  if (workspaces.value.length > 0) return void selectWorkspace(workspaces.value[0].id)
  selectedWorkspaceId.value = ''
  resetForm()
}
async function loadAll() {
  loading.value = true
  try {
    const [workspaceItems, pluginItems, entries] = await Promise.all([ListWorkspaces(), GetInstalledPlugins(), GetGlobalEnabled()])
    workspaces.value = workspaceItems || []
    installedPlugins.value = (pluginItems || []).slice().sort((a, b) => a.name.localeCompare(b.name))
    globalEntries.value = entries || []
    applyRouteSelection()
  } catch (error) {
    showError(`加载工作区数据失败: ${error}`)
  } finally {
    loading.value = false
  }
}
function toggleTool(value: string) {
  const set = new Set(form.tools)
  if (set.has(value)) {
    if (set.size === 1) return
    set.delete(value)
  } else {
    set.add(value)
  }
  form.tools = [...set]
}
async function saveWorkspace() {
  if (!canSaveWorkspace.value) return showError('请填写工作目录，并至少选择一个工具目标')
  savingWorkspace.value = true
  try {
    const name = form.name.trim() || basename(form.path)
    const path = form.path.trim()
    const tools = [...form.tools]
    const saved = selectedWorkspaceId.value
      ? await UpdateWorkspace(selectedWorkspaceId.value, name, path, tools)
      : await CreateWorkspace(name, path, tools)
    showSuccess(selectedWorkspaceId.value ? '工作区已更新' : '工作区已创建')
    await loadAll(); await selectWorkspace(saved.id)
  } catch (error) {
    showError(`保存工作区失败: ${error}`)
  } finally {
    savingWorkspace.value = false
  }
}
async function confirmDeleteWorkspace() {
  if (!selectedWorkspace.value) return
  actionLoading.value = true
  try {
    await DeleteWorkspace(selectedWorkspace.value.id)
    showDeleteDialog.value = false
    lastAction.value = null
    showSuccess('工作区已删除')
    await loadAll()
  } catch (error) {
    showError(`删除工作区失败: ${error}`)
  } finally {
    actionLoading.value = false
  }
}
function togglePlugin(pluginId: string) {
  const detail = availablePlugins.value.find(item => item.id === pluginId)
  if (!detail || !canSelectPlugin(detail)) return
  const draft = getDraft(pluginId)
  if (draft.selected) return drafts.value = { ...drafts.value, [pluginId]: { ...draft, selected: false } }
  let mode: Mode = draft.mode
  if (mode === 'all' && !canUseWhole(detail)) mode = 'partial'
  if (mode === 'partial' && !canUsePartial(detail)) mode = 'all'
  if (!draft.selectedKeys.length && mode === 'all' && !canUseWhole(detail)) mode = 'partial'
  drafts.value = { ...drafts.value, [pluginId]: { selected: true, mode, selectedKeys: normalizePartialKeys(detail, draft.selectedKeys) } }
}
function setMode(pluginId: string, mode: Mode) {
  const detail = availablePlugins.value.find(item => item.id === pluginId)
  if (!detail) return
  if (mode === 'all' && !canUseWhole(detail)) return
  if (mode === 'partial' && !canUsePartial(detail)) return
  const draft = getDraft(pluginId)
  drafts.value = { ...drafts.value, [pluginId]: { ...draft, selected: true, mode, selectedKeys: normalizePartialKeys(detail, draft.selectedKeys) } }
}
function toggleSubItem(pluginId: string, item: plugin.SubItem) {
  if (!item.selectable) return
  const draft = getDraft(pluginId)
  const key = keyOf(item)
  drafts.value = { ...drafts.value, [pluginId]: { ...draft, selected: true, mode: 'partial', selectedKeys: draft.selectedKeys.includes(key) ? draft.selectedKeys.filter(entry => entry !== key) : [...draft.selectedKeys, key] } }
}
function subItemSelected(pluginId: string, item: plugin.SubItem) { return getDraft(pluginId).selectedKeys.includes(keyOf(item)) }
function toggleSubItemGroup(pluginId: string, type: string) {
  const key = subItemGroupKey(pluginId, type)
  expandedSubItemGroups.value[key] = !isSubItemGroupExpanded(pluginId, type)
}
function groupedSubItems(detail: { subItems?: plugin.SubItem[] }): SubItemGroup[] {
  const groups = new Map<string, SubItemGroup>()
  for (const item of enabledSubItems(detail)) {
    let group = groups.get(item.type)
    if (!group) {
      group = { type: item.type, label: subItemTypeLabel(item.type), items: [] }
      groups.set(item.type, group)
    }
    group.items.push(item)
  }
  return [...groups.values()].sort((left, right) => {
    const leftIndex = subItemTypeOrder.indexOf(left.type)
    const rightIndex = subItemTypeOrder.indexOf(right.type)
    const a = leftIndex === -1 ? Number.MAX_SAFE_INTEGER : leftIndex
    const b = rightIndex === -1 ? Number.MAX_SAFE_INTEGER : rightIndex
    return a - b || left.label.localeCompare(right.label)
  })
}
function subItemStateLabel(pluginId: string, item: plugin.SubItem) {
  if (item.globallyEnabled) return '全局已启用'
  if (!item.selectable) return '不可选择'
  return subItemSelected(pluginId, item) ? '已选中' : '未选中'
}
function subItemToggleActive(pluginId: string, item: plugin.SubItem) {
  if (item.globallyEnabled) return true
  return item.selectable && subItemSelected(pluginId, item)
}
function toggleWorkspaceSubItem(pluginId: string, item: plugin.SubItem) {
  if (!item.selectable) return
  toggleSubItem(pluginId, item)
}
function hasEmptyPartialSelection(pluginId: string) {
  const draft = getDraft(pluginId)
  return draft.selected && draft.mode === 'partial' && draft.selectedKeys.length === 0
}
async function saveWorkspacePlugins() {
  if (!selectedWorkspaceId.value || !canSaveSelection.value) return showError('请先完成当前插件选择')
  savingSelection.value = true
  try {
    const payload = availablePlugins.value.flatMap(item => {
      const draft = getDraft(item.id)
      if (!draft.selected) return []
      if (draft.mode === 'all') return [workspace.WorkspacePlugin.createFrom({ pluginId: item.id, enabledSubItems: [], deployScope: 'workspace' })]
      return [workspace.WorkspacePlugin.createFrom({
        pluginId: item.id,
        enabledSubItems: enabledSubItems(item)
          .filter(subItem => draft.selectedKeys.includes(keyOf(subItem)))
          .map(subItem => plugin.SubItemRef.createFrom({ type: subItem.type, name: subItem.name })),
        deployScope: 'workspace'
      })]
    })
    await SetWorkspacePlugins(selectedWorkspaceId.value, payload)
    showSuccess('工作区插件选择已保存')
    await loadAll(); await selectWorkspace(selectedWorkspaceId.value)
  } catch (error) {
    showError(`保存工作区插件失败: ${error}`)
  } finally {
    savingSelection.value = false
  }
}
function setAction(kind: ActionKind, label: string, result: ActionResult) {
  lastAction.value = { kind, label, result }
  const warnings = result.warnings?.length || 0
  const conflicts = 'conflicts' in result ? result.conflicts.length : 0
  if (conflicts > 0) showError(`${label}完成，但存在 ${conflicts} 个冲突`)
  else if (warnings > 0) showInfo(`${label}完成，同时返回 ${warnings} 条后端警告`)
  else showSuccess(`${label}完成`)
}
async function runAction(kind: 'build' | 'sync' | 'clean') {
  if (!selectedWorkspaceId.value) return
  actionLoading.value = true
  try {
    if (kind === 'build') setAction(kind, '部署工作区', await BuildScaffold(selectedWorkspaceId.value))
    else if (kind === 'sync') setAction(kind, '同步工作区', await SyncWorkspace(selectedWorkspaceId.value))
    else setAction(kind, '清理工作区', await CleanWorkspace(selectedWorkspaceId.value))
  } catch (error) {
    showError(`执行工作区操作失败: ${error}`)
  } finally {
    actionLoading.value = false
  }
}
function conflictLabel(value: string) { return ({ target_path: '目标路径冲突', user_file: '用户文件冲突', mcp_key: 'MCP 键冲突', modified_file: '托管文件已修改' } as Record<string, string>)[value] || value }
async function handleGlobalSaved(result: workspace.DeployResult) { showGlobalDialog.value = false; setAction('global', '全局启用配置', result); await loadAll() }
onMounted(() => { void loadAll() })
</script>

<template>
  <div class="workspaces-view">
    <div class="loading-bar" v-if="busy"></div>
    <div class="toolbar">
      <div>
        <h2 class="section-title">工作区管理</h2>
        <p class="section-hint">保存工作目录、插件组合与部署结果；后端返回的警告和冲突会直接展示出来。</p>
      </div>
      <div class="toolbar-actions">
        <button class="btn secondary" @click="loadAll" :disabled="busy">刷新</button>
        <button class="btn secondary" @click="showGlobalDialog = true" :disabled="busy">全局启用</button>
        <button class="btn primary" @click="selectedWorkspaceId = ''; resetForm()" :disabled="busy">新建工作区</button>
      </div>
    </div>

    <div class="workspace-grid">
      <div class="card list-card">
        <div class="card-header"><div><h3 class="card-title">{{ workspaces.length }} 个工作区</h3><p class="card-hint">选择一个工作区继续编辑。</p></div></div>
        <div class="list-body" v-if="workspaces.length">
          <button v-for="item in workspaces" :key="item.id" class="workspace-item" :class="{ active: selectedWorkspaceId === item.id }" @click="selectWorkspace(item.id)">
            <div class="row-between"><span class="workspace-name">{{ item.name || basename(item.path) }}</span><span class="mini-badge">{{ item.plugins.length }} 个插件</span></div>
            <div class="workspace-path" :title="item.path">{{ item.path }}</div>
            <div class="tool-row"><span v-for="tool in item.tools" :key="tool" class="tool-badge">{{ toolLabel(tool) }}</span></div>
          </button>
        </div>
        <div class="empty-state" v-else><p class="empty-title">还没有工作区</p><p class="section-hint">先创建一个工作区，再选择插件和部署目标。</p></div>
      </div>

      <div class="main-column">
        <div class="card">
          <div class="card-header"><div><h3 class="card-title">{{ isCreateMode ? '新建工作区' : '编辑工作区' }}</h3><p class="card-hint">当前仅 Claude 会实际部署，其他工具会保留配置边界并在执行时返回警告。</p></div></div>
          <div class="card-body form-body">
            <div class="form-row">
              <div class="field"><label>工作区名称</label><input v-model="form.name" class="input-field" type="text" placeholder="默认使用目录名" /></div>
              <div class="field field-wide"><label>工作目录</label><div class="path-row"><input v-model="form.path" class="input-field" type="text" placeholder="选择或输入工作目录" /><button class="btn icon-btn" @click="browseWorkspaceDirectory" :disabled="busy">浏览</button></div></div>
            </div>
            <div class="field"><label>部署目标</label><div class="tool-grid"><button v-for="tool in toolOptions" :key="tool.value" class="tool-pill" :class="{ active: form.tools.includes(tool.value) }" @click="toggleTool(tool.value)"><span>{{ tool.label }}</span><small>{{ tool.desc }}</small></button></div></div>
          </div>
          <div class="card-footer"><button class="btn secondary" @click="resetForm(form.path)" :disabled="busy">重置</button><button class="btn danger" v-if="!isCreateMode" @click="showDeleteDialog = true" :disabled="busy">删除</button><button class="btn primary" @click="saveWorkspace" :disabled="busy || !canSaveWorkspace">{{ savingWorkspace ? '保存中...' : (isCreateMode ? '创建工作区' : '保存修改') }}</button></div>
        </div>

        <div class="card" v-if="selectedWorkspace">
          <div class="card-header actions-header"><div><h3 class="card-title">插件选择</h3><p class="card-hint">已选择 {{ selectedCount }} 个插件。整插件全局启用会直接隐藏插件；局部全局启用的子项会灰显并不可选。</p></div><div class="header-buttons"><button class="btn secondary small" @click="saveWorkspacePlugins" :disabled="busy || !canSaveSelection">保存选择</button><button class="btn secondary small" @click="runAction('build')" :disabled="busy">部署</button><button class="btn secondary small" @click="runAction('sync')" :disabled="busy">同步</button><button class="btn danger small" @click="runAction('clean')" :disabled="busy">清理</button></div></div>
          <div class="card-body panel-body" v-if="availablePlugins.length">
            <div v-for="item in availablePlugins" :key="item.id" class="plugin-card" :class="{ muted: !canSelectPlugin(item) }">
              <div class="row-between gap-start"><div class="plugin-main"><div class="plugin-title-row"><h4 class="plugin-name">{{ item.name }}</h4><span class="badge" :class="typeClass(item.pluginType)">{{ pluginTypeLabel(item.pluginType) }}</span><span class="mini-badge">{{ item.version }}</span><span class="mini-badge" v-if="item.scope">{{ item.scope }}</span><span class="mini-badge success" v-if="item.hasClaudeMd">CLAUDE.md</span></div><p class="section-hint">{{ item.manifest?.description || '无描述信息' }}</p></div><button class="btn" :class="getDraft(item.id).selected ? 'primary' : 'secondary'" :disabled="busy || !canSelectPlugin(item)" @click="togglePlugin(item.id)">{{ getDraft(item.id).selected ? '已选择' : '选择插件' }}</button></div>
              <div class="notice" v-if="hasGlobalSubItems(item)">该插件已有全局启用子项，整插件模式已禁用；灰色条目由全局配置持有。</div>
              <div class="notice muted" v-else-if="!canSelectPlugin(item)">该插件当前没有可供工作区接管的子项。</div>
              <div class="selection-box" v-if="getDraft(item.id).selected">
                <div class="mode-row"><button class="mode-pill" :class="{ active: getDraft(item.id).mode === 'all' }" :disabled="!canUseWhole(item)" @click="setMode(item.id, 'all')">整插件</button><button class="mode-pill" :class="{ active: getDraft(item.id).mode === 'partial' }" :disabled="!canUsePartial(item)" @click="setMode(item.id, 'partial')">指定子项</button></div>
                <p class="section-hint" v-if="getDraft(item.id).mode === 'all'">整插件模式会部署当前可用内容；如果插件携带 CLAUDE.md，也会一起写入工作区。</p>
                <template v-else>
                  <p class="section-hint">以下切换仅修改当前草稿，点击“保存选择”后才会真正生效。</p>
                  <div class="subitem-groups">
                  <div class="subitem-group" v-for="group in groupedSubItems(item)" :key="subItemGroupKey(item.id, group.type)">
                    <button type="button" class="subitem-group-header" @click="toggleSubItemGroup(item.id, group.type)">
                      <span class="subitem-group-meta">
                        <span class="subitem-group-title">{{ group.label }}</span>
                        <span class="badge subitem-count-badge">{{ group.items.length }}</span>
                      </span>
                      <svg :class="['chevron', { expanded: isSubItemGroupExpanded(item.id, group.type) }]" viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                        <polyline points="6 9 12 15 18 9"></polyline>
                      </svg>
                    </button>
                    <div class="detail-list" v-if="isSubItemGroupExpanded(item.id, group.type)">
                      <div class="detail-item with-toggle" v-for="subItem in group.items" :key="keyOf(subItem)" :class="{ locked: !subItem.selectable, dim: subItem.globallyEnabled }">
                        <div class="detail-item-copy">
                          <div class="item-name-line">
                            <span class="item-name">{{ subItem.name }}</span>
                            <span class="badge">{{ subItemTypeLabel(subItem.type) }}</span>
                            <span class="badge" v-if="subItem.globallyEnabled">全局已启用</span>
                          </div>
                          <span class="item-desc">{{ subItem.path }}</span>
                        </div>
                        <div class="detail-item-actions">
                          <span class="toggle-label" :class="{ 'text-enabled': subItemToggleActive(item.id, subItem), 'text-disabled': !subItemToggleActive(item.id, subItem) }">{{ subItemStateLabel(item.id, subItem) }}</span>
                          <button class="ios-toggle" :class="{ active: subItemToggleActive(item.id, subItem) }" :disabled="!subItem.selectable" @click="toggleWorkspaceSubItem(item.id, subItem)"></button>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div class="notice muted" v-if="hasEmptyPartialSelection(item.id)">局部模式至少保留一个子项，当前选择还不能保存。</div>
                  </div>
                </template>
              </div>
            </div>
          </div>
          <div class="empty-state" v-else><p class="empty-title">当前没有可选插件</p><p class="section-hint">请先安装并启用插件，或检查是否已被全局启用完全接管。</p></div>
        </div>

        <div class="card" v-if="lastAction">
          <div class="card-header"><div><h3 class="card-title">最近一次执行结果</h3><p class="card-hint">{{ lastAction.label }}</p></div></div>
          <div class="card-body result-body">
            <div class="metrics"><div class="metric"><span>部署条目</span><strong>{{ 'deployed' in lastAction.result ? lastAction.result.deployed.length : 0 }}</strong></div><div class="metric"><span>移除条目</span><strong>{{ lastAction.result.removed.length }}</strong></div><div class="metric"><span>警告</span><strong>{{ lastAction.result.warnings.length }}</strong></div><div class="metric danger" v-if="'conflicts' in lastAction.result && lastAction.result.conflicts.length"><span>冲突</span><strong>{{ lastAction.result.conflicts.length }}</strong></div></div>
            <div class="result-section" v-if="lastAction.result.warnings.length"><h4>后端警告</h4><ul><li v-for="warning in lastAction.result.warnings" :key="warning">{{ warning }}</li></ul></div>
            <div class="result-section" v-if="'conflicts' in lastAction.result && lastAction.result.conflicts.length"><h4>冲突详情</h4><div class="conflict" v-for="conflict in lastAction.result.conflicts" :key="`${conflict.type}-${conflict.targetPath || conflict.message}`"><div class="conflict-title">{{ conflictLabel(conflict.type) }}</div><div class="section-hint" v-if="conflict.targetPath">{{ conflict.targetPath }}</div><div class="section-hint">{{ conflict.message }}</div></div></div>
          </div>
        </div>
      </div>
    </div>
    <GlobalEnabledDialog v-if="showGlobalDialog" :installed-plugins="installedPlugins" :entries="globalEntries" @close="showGlobalDialog = false" @saved="handleGlobalSaved" />
    <transition name="dialog-fade"><div class="dialog-overlay" v-if="showDeleteDialog"><div class="dialog" @click.stop><h3 class="card-title">删除工作区</h3><p class="section-hint">删除前会先尝试清理当前工作区的托管文件，此操作不可恢复。</p><div class="toolbar-actions"><button class="btn secondary" @click="showDeleteDialog = false" :disabled="busy">取消</button><button class="btn danger" @click="confirmDeleteWorkspace" :disabled="busy">{{ actionLoading ? '处理中...' : '确认删除' }}</button></div></div></div></transition>
  </div>
</template>

<style scoped>
.workspaces-view,.main-column,.form-body,.panel-body,.selection-box,.subitem-list,.result-body,.result-section,.plugin-main{display:flex;flex-direction:column;gap:14px}
.workspaces-view{position:relative}
.toolbar,.toolbar-actions,.row-between,.tool-row,.tool-grid,.form-row,.path-row,.header-buttons,.mode-row,.metrics{display:flex;gap:10px;align-items:center}
.toolbar,.row-between{justify-content:space-between}
.toolbar{align-items:flex-start}
.workspace-grid{display:grid;grid-template-columns:minmax(280px,320px) minmax(0,1fr);gap:20px}
.card{background:#1a1f2e;border:1px solid #2a2f3e;border-radius:8px;overflow:hidden}
.card-header,.card-footer{padding:16px 20px;background:rgba(42,47,62,.3);border-bottom:1px solid #2a2f3e}
.card-footer{background:transparent;border-bottom:none;border-top:1px solid #2a2f3e;display:flex;justify-content:flex-end;gap:10px}
.card-body{padding:18px 20px}
.section-title,.card-title,.empty-title,.workspace-name,.plugin-name,.subitem-title{margin:0;color:#e0e6ed}
.section-title{font-size:18px;font-weight:600}
.card-title,.workspace-name,.plugin-name{font-size:16px;font-weight:600}
.section-hint,.card-hint,.workspace-path{margin:0;color:#8899aa;font-size:13px;line-height:1.5}
.loading-bar{position:absolute;top:0;left:0;right:0;height:2px;background:linear-gradient(90deg,transparent,#4fc3f7,transparent);animation:loading-slide 1.2s ease-in-out infinite;z-index:10}
@keyframes loading-slide{0%{transform:translateX(-100%)}100%{transform:translateX(100%)}}
.list-body{padding:16px;display:flex;flex-direction:column;gap:12px}
.workspace-item,.plugin-card,.subitem-row,.tool-pill,.mode-pill,.btn{font-family:inherit}
.workspace-item,.plugin-card,.subitem-row{background:#0f1219;border:1px solid #2a2f3e;border-radius:8px;color:inherit;text-align:left}
.workspace-item{padding:14px 16px;display:flex;flex-direction:column;gap:10px;cursor:pointer;transition:.15s}
.workspace-item.active,.subitem-row.active,.mode-pill.active,.tool-pill.active{border-color:#4fc3f7;background:rgba(79,195,247,.08)}
.workspace-item:hover,.subitem-row:hover:not(:disabled),.tool-pill:hover,.btn:hover:not(:disabled){border-color:#3a4f5e}
.workspace-path{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.field{display:flex;flex-direction:column;gap:8px;flex:1}
.field-wide{flex:2}
.field label{color:#ccd6e0;font-size:13px;font-weight:600}
.input-field{width:100%;box-sizing:border-box;padding:10px 12px;background:#0f1219;border:1px solid #2a2f3e;border-radius:6px;color:#e0e6ed;font-size:14px;outline:none}
.input-field:focus{border-color:#4fc3f7}
.tool-grid{flex-wrap:wrap}
.tool-pill{min-width:148px;padding:10px 12px;display:flex;flex-direction:column;align-items:flex-start;cursor:pointer;color:#8899aa}
.tool-pill small{font-size:12px;line-height:1.4}
.actions-header{align-items:flex-start}
.header-buttons{flex-wrap:wrap;justify-content:flex-end}
.badge,.mini-badge,.tool-badge{display:inline-flex;align-items:center;justify-content:center;padding:4px 8px;border-radius:999px;font-size:12px;font-weight:600;background:rgba(136,153,170,.12);color:#aab8c5}
.mini-badge.success{background:rgba(102,187,106,.12);color:#66bb6a}
.type-integration{background:rgba(79,195,247,.12);color:#4fc3f7}.type-hybrid{background:rgba(255,167,38,.12);color:#ffa726}.type-skill,.type-hook,.type-command,.type-agent,.type-mcp,.type-unknown{background:rgba(136,153,170,.12);color:#ccd6e0}
.notice{padding:10px 12px;border-radius:6px;border:1px solid rgba(79,195,247,.18);background:rgba(79,195,247,.08);color:#a7cde0;font-size:13px;line-height:1.5}
.notice.muted,.plugin-card.muted{opacity:.82}
.selection-box{padding-top:4px}
.mode-pill,.btn{border:1px solid #2a2f3e;background:transparent;color:#e0e6ed;border-radius:6px;cursor:pointer;transition:.15s}
.mode-pill{padding:6px 12px;border-radius:999px;color:#8899aa}
.mode-pill:disabled,.btn:disabled,.ios-toggle:disabled{opacity:.5;cursor:not-allowed}
.subitem-groups{display:flex;flex-direction:column;gap:12px}
.subitem-group{border:1px solid #2a2f3e;border-radius:8px;overflow:hidden;background:#0f1219}
.subitem-group-header{width:100%;display:flex;justify-content:space-between;align-items:center;gap:12px;padding:11px 14px;background:rgba(42,47,62,.32);border:none;color:#ccd6e0;cursor:pointer;text-align:left}
.subitem-group-header:hover{background:rgba(42,47,62,.5)}
.subitem-group-meta{display:flex;align-items:center;gap:8px;min-width:0}
.subitem-group-title{font-size:13px;font-weight:600;color:#e0e6ed}
.subitem-count-badge{background:rgba(136,153,170,.12);color:#8899aa;font-size:11px}
.detail-list{display:flex;flex-direction:column;gap:0}
.detail-item.with-toggle{display:flex;justify-content:space-between;align-items:center;gap:16px;padding:12px 14px;border-top:1px solid #2a2f3e;background:#0f1219}
.detail-item.locked{border-style:dashed}
.detail-item.dim{opacity:.68}
.detail-item-copy{display:flex;flex-direction:column;gap:6px;min-width:0;flex:1}
.item-name-line{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.item-name{font-weight:600;color:#e0e6ed;min-width:0}
.item-desc{margin:0;color:#8899aa;font-size:13px;line-height:1.5}
.detail-item-actions{display:flex;align-items:center;gap:10px;flex-shrink:0}
.toggle-label{font-size:13px;font-weight:600;white-space:nowrap}
.text-enabled{color:#66bb6a}
.text-disabled{color:#8899aa}
.ios-toggle{position:relative;width:44px;height:24px;background:#2a2f3e;border-radius:24px;cursor:pointer;transition:background .2s ease;border:none;outline:none;flex-shrink:0}
.ios-toggle.active{background:#66bb6a}
.ios-toggle::after{content:'';position:absolute;top:2px;left:2px;width:20px;height:20px;background:#fff;border-radius:50%;transition:transform .2s cubic-bezier(.25,.8,.25,1),background .2s;box-shadow:0 2px 4px rgba(0,0,0,.2)}
.ios-toggle.active::after{transform:translateX(20px)}
.chevron{transition:transform .18s ease}
.chevron.expanded{transform:rotate(180deg)}
.metrics{flex-wrap:wrap}.metric{min-width:120px;padding:12px 14px;border:1px solid #2a2f3e;border-radius:8px;background:#0f1219;display:flex;flex-direction:column;gap:6px;color:#8899aa}
.metric strong{font-size:20px;color:#e0e6ed}.metric.danger strong,.conflict-title{color:#ef5350}
.result-section h4{margin:0;color:#e0e6ed;font-size:14px}
.result-section ul{margin:0;padding-left:18px;color:#ccd6e0;display:flex;flex-direction:column;gap:8px}
.conflict{padding:12px 14px;border-radius:8px;border:1px solid rgba(239,83,80,.22);background:rgba(239,83,80,.06)}
.empty-state{padding:28px 20px;text-align:center;display:flex;flex-direction:column;gap:8px}
.btn{display:inline-flex;align-items:center;justify-content:center;padding:8px 16px;white-space:nowrap}
.btn.primary{background:#4fc3f7;border-color:#4fc3f7;color:#0f1219}.btn.primary:hover:not(:disabled){background:#7bd4f9;border-color:#7bd4f9}
.btn.secondary:hover:not(:disabled){color:#4fc3f7}.btn.danger{color:#ef5350;border-color:#ef5350}.btn.danger:hover:not(:disabled){background:rgba(239,83,80,.1)}
.btn.small{padding:6px 12px;font-size:13px}.icon-btn{min-width:52px}
.dialog-overlay{position:fixed;inset:0;background:rgba(15,18,25,.82);display:flex;align-items:center;justify-content:center;z-index:120;backdrop-filter:blur(4px)}
.dialog{width:min(420px,calc(100vw - 32px));padding:24px;background:#1a1f2e;border:1px solid #2a2f3e;border-radius:8px;display:flex;flex-direction:column;gap:16px;box-shadow:0 12px 40px rgba(0,0,0,.4)}
.dialog-fade-enter-active,.dialog-fade-leave-active{transition:opacity .15s ease}.dialog-fade-enter-from,.dialog-fade-leave-to{opacity:0}
@media (max-width:1080px){.workspace-grid{grid-template-columns:1fr}}
@media (max-width:720px){.toolbar,.form-row,.actions-header,.row-between,.detail-item.with-toggle{flex-direction:column;align-items:stretch}.toolbar-actions,.header-buttons,.card-footer{justify-content:stretch}.toolbar-actions>*,.header-buttons>*,.card-footer>*{flex:1}}
</style>
