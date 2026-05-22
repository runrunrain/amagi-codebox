<script lang="ts" setup>
import { ref, computed, onMounted } from 'vue'
import { useToast } from '../composables/useToast'
import {
  GetMarketplaces,
  GetInstalledPlugins,
  GetPluginDetail,
  GetAvailablePlugins,
  InstallPlugin,
  UninstallPlugin,
  EnablePlugin,
  DisablePlugin,
  UpdatePlugin,
  UpdateMarketplace,
  AddMarketplace,
  AnalyzePluginType,
  GetPluginSubItems,
  GetPluginSubItemStates,
  SetSubItemEnabled
} from '../../wailsjs/go/plugin/Service'

const { showSuccess, showError } = useToast()

const loading = ref(false)
const marketplaces = ref<any[]>([])
const installedPlugins = ref<any[]>([])
const availablePlugins = ref<any[]>([])
const expandedInstalledGroups = ref<Record<string, boolean>>({})
const installingPlugins = ref<Record<string, boolean>>({})
const selectedMarketplace = ref('')
const selectedDetailItems = ref<Record<string, string>>({})

const expandedPluginId = ref<string | null>(null)
const pluginDetails = ref<Record<string, any>>({})
const loadingDetails = ref<Record<string, boolean>>({})

const searchQuery = ref('')
const sortBy = ref<'installCount' | 'name'>('installCount')

const subItemKey = (item: { type: string; name: string }) => `${item.type}:${item.name}`
const pluginTypeLabel = (value = 'unknown') => ({ integration: '集成', hybrid: '混合', skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', unknown: '未知' } as Record<string, string>)[value] || value
const pluginTypeClass = (value?: string) => `type-${value || 'unknown'}`
const subItemTypeLabel = (value: string) => ({ skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', claude: 'Claude' } as Record<string, string>)[value] || value
const getMcpServerNames = (detail: any) => Object.keys(detail?.mcpServers || {})
const hasDetailResources = (detail: any) => Boolean(detail?.skills?.length || detail?.agents?.length || detail?.commands?.length || detail?.hooks?.length || getMcpServerNames(detail).length || detail?.subItems?.length || detail?.hasClaudeMd)
type DetailResourceType = 'skill' | 'agent' | 'command' | 'hook' | 'mcp' | 'claude'
type DetailEntry = { key: string; name: string; description?: string; badge?: string; path?: string; subItem: any | null }
type DetailNavItem = DetailEntry & { type: DetailResourceType; typeLabel: string; path?: string }
type McpServerSummary = {
  name: string
  transport: string
  command: string
  argsCount: number
  hasRemoteEndpoint: boolean
  hasEnv: boolean
  hasHeaders: boolean
  hasSensitiveFields: boolean
}
const sensitiveMcpKeyPattern = /(secret|key|token|password|authorization|cookie|env|headers)/i

function hasSensitiveMcpKeys(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false
  if (Array.isArray(value)) return value.some(item => hasSensitiveMcpKeys(item))
  return Object.entries(value as Record<string, unknown>).some(([key, nested]) => sensitiveMcpKeyPattern.test(key) || hasSensitiveMcpKeys(nested))
}

function safeExecutableName(value: unknown) {
  if (typeof value !== 'string' || !value.trim()) return '-'
  const normalized = value.trim().replace(/\\/g, '/')
  return normalized.split('/').filter(Boolean).pop() || normalized
}

function summarizeMcpServer(detail: any, serverName: string): McpServerSummary {
  const server = detail?.mcpServers?.[serverName]
  if (!server || typeof server !== 'object' || Array.isArray(server)) {
    return {
      name: serverName,
      transport: server ? typeof server : 'configured',
      command: '-',
      argsCount: 0,
      hasRemoteEndpoint: false,
      hasEnv: false,
      hasHeaders: false,
      hasSensitiveFields: false
    }
  }
  const config = server as Record<string, any>
  const transport = config.type || config.transport || (config.command ? 'stdio' : (config.url || config.endpoint ? 'remote' : 'configured'))
  return {
    name: serverName,
    transport: String(transport),
    command: safeExecutableName(config.command || config.executable || config.path),
    argsCount: Array.isArray(config.args) ? config.args.length : 0,
    hasRemoteEndpoint: Boolean(config.url || config.endpoint),
    hasEnv: Boolean(config.env),
    hasHeaders: Boolean(config.headers),
    hasSensitiveFields: hasSensitiveMcpKeys(config)
  }
}

function selectedMcpServerSummary(pluginId: string, detail: any) {
  const selected = selectedDetailItem(pluginId, detail)
  if (!selected || selected.type !== 'mcp') return null
  return summarizeMcpServer(detail, selected.name)
}

function findSubItem(detail: any, type: string, name: string) {
  return (detail?.subItems || []).find((item: any) => item.type === type && item.name === name) || null
}
function detailEntries(detail: any, type: string): DetailEntry[] {
  switch (type) {
    case 'skill':
      return (detail?.skills || []).map((item: any) => ({ key: item.name, name: item.name, description: item.description, path: item.filePath || item.path, subItem: findSubItem(detail, 'skill', item.name) }))
    case 'agent':
      return (detail?.agents || []).map((item: any) => ({ key: item.name, name: item.name, description: item.description, path: item.filePath || item.path, subItem: findSubItem(detail, 'agent', item.name) }))
    case 'command':
      return (detail?.commands || []).map((item: any) => ({ key: item.name, name: item.name, description: item.description || item.filePath || item.path, path: item.filePath || item.path, subItem: findSubItem(detail, 'command', item.name) }))
    case 'hook':
      return (detail?.hooks || []).map((item: any) => ({ key: item.name || `${item.event}:${item.type}`, name: item.name ? `${item.event} / ${item.name}` : item.event, description: item.command || item.filePath, path: item.filePath, badge: item.type, subItem: findSubItem(detail, 'hook', item.name) }))
    case 'mcp':
      return getMcpServerNames(detail).map((name: string) => ({ key: name, name, subItem: findSubItem(detail, 'mcp', name) }))
    default:
      return []
  }
}

function buildDetailNavItems(detail: any): DetailNavItem[] {
  const order: DetailResourceType[] = ['skill', 'agent', 'command', 'hook', 'mcp']
  const items = order.flatMap(type => detailEntries(detail, type).map(entry => ({
    ...entry,
    type,
    typeLabel: subItemTypeLabel(type),
    key: `${type}:${entry.key}`
  })))
  if (detail?.hasClaudeMd) {
    items.push({
      key: 'claude:CLAUDE.md',
      type: 'claude',
      typeLabel: 'Claude',
      name: 'CLAUDE.md',
      description: detail.claudeMdPath || '插件根目录',
      path: detail.claudeMdPath,
      subItem: null
    })
  }
  return items
}

function selectedDetailItem(pluginId: string, detail: any) {
  const items = buildDetailNavItems(detail)
  if (items.length === 0) return null
  const selectedKey = selectedDetailItems.value[pluginId]
  return items.find(item => item.key === selectedKey) || items[0]
}

function selectDetailItem(pluginId: string, item: DetailNavItem) {
  selectedDetailItems.value[pluginId] = item.key
}
function mergeSubItemStates(items: any[], state: any) {
  const disabled = new Set((state?.disabledSubItems || []).map((ref: any) => subItemKey(ref)))
  return (items || []).map((item: any) => ({ ...item, enabled: !disabled.has(subItemKey(item)) }))
}

const availableByMarketplace = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  let filtered = availablePlugins.value
  if (query) {
    const tokens = query.split(/\s+/).filter(Boolean)
    filtered = availablePlugins.value.filter((p: any) => {
      const name = (p.name || '').toLowerCase()
      const desc = (p.description || '').toLowerCase()
      const text = name + ' ' + desc
      return tokens.every(t => text.includes(t))
    })
  }
  const groups: Record<string, { name: string; plugins: any[] }> = {}
  for (const p of filtered) {
    const mkt = p.marketplaceName || 'unknown'
    if (!groups[mkt]) {
      groups[mkt] = { name: mkt, plugins: [] }
    }
    groups[mkt].plugins.push(p)
  }
  for (const g of Object.values(groups)) {
    if (sortBy.value === 'installCount') {
      g.plugins.sort((a: any, b: any) => (b.installCount || 0) - (a.installCount || 0))
    } else {
      g.plugins.sort((a: any, b: any) => (a.name || '').localeCompare(b.name || ''))
    }
  }
  return Object.values(groups).sort((a, b) => a.name.localeCompare(b.name))
})

const filteredAvailableCount = computed(() => {
  return availableByMarketplace.value.reduce((sum, g) => sum + g.plugins.length, 0)
})

const marketplaceConsoleItems = computed(() => {
  const byName = new Map(availableByMarketplace.value.map(group => [group.name, group]))
  const items = marketplaces.value.map((marketplace: any) => {
    const name = marketplace.name || marketplace.repo || marketplace.url || 'unknown'
    return {
      ...marketplace,
      name,
      plugins: byName.get(name)?.plugins || []
    }
  })
  for (const group of availableByMarketplace.value) {
    if (!items.some((item: any) => item.name === group.name)) {
      items.push({ name: group.name, source: 'marketplace', plugins: group.plugins })
    }
  }
  return items.sort((a: any, b: any) => a.name.localeCompare(b.name))
})

const selectedMarketplaceItem = computed(() => {
  const items = marketplaceConsoleItems.value
  if (!items.length) return null
  return items.find((item: any) => item.name === selectedMarketplace.value) || items[0]
})

const selectedAvailablePlugins = computed(() => selectedMarketplaceItem.value?.plugins || [])

const installedByMarketplace = computed(() => {
  const groups: Record<string, { name: string; plugins: any[] }> = {}
  for (const p of installedPlugins.value) {
    const mkt = p.marketplace || 'local'
    if (!groups[mkt]) {
      groups[mkt] = { name: mkt, plugins: [] }
    }
    groups[mkt].plugins.push(p)
  }
  for (const g of Object.values(groups)) {
    g.plugins.sort((a: any, b: any) => {
      if (a.enabled === b.enabled) return a.name.localeCompare(b.name)
      return a.enabled ? -1 : 1
    })
  }
  return Object.values(groups).sort((a, b) => a.name.localeCompare(b.name))
})
// Add marketplace dialog
const addMarketDialog = ref({
  show: false,
  source: '',
  submitting: false
})

// Dialog states
const confirmDialog = ref<{
  show: boolean;
  title: string;
  message: string;
  action: () => Promise<void>;
}>({
  show: false,
  title: '',
  message: '',
  action: async () => {}
})

async function loadData() {
  loading.value = true
  try {
    marketplaces.value = await GetMarketplaces() || []
    const installed = await GetInstalledPlugins() || []
    installedPlugins.value = await Promise.all(installed.map(async (plugin: any) => {
      try {
        return { ...plugin, pluginType: await AnalyzePluginType(plugin.id) }
      } catch {
        return { ...plugin, pluginType: 'unknown' }
      }
    }))
    try {
      const all = await GetAvailablePlugins() || []
      const installedIds = new Set(installedPlugins.value.map((p: any) => p.id))
      availablePlugins.value = all.filter((p: any) => !installedIds.has(p.pluginId))
    } catch {
      availablePlugins.value = []
    }
  } catch (err) {
    showError('加载数据失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function updateMarketplace(name: string) {
  loading.value = true
  try {
    const res = await UpdateMarketplace(name)
    if (res && !res.success) {
      showError(`更新市场 ${name} 失败: ` + res.error)
    } else {
      showSuccess(`市场 ${name} 更新成功`)
      await loadData()
    }
  } catch (err) {
    showError('更新失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function updateAllMarketplaces() {
  loading.value = true
  try {
    let hasError = false
    for (const m of marketplaces.value) {
      const res = await UpdateMarketplace(m.name)
      if (res && !res.success) hasError = true
    }
    if (hasError) {
      showError('部分市场更新失败，请查看日志')
    } else {
      showSuccess('所有市场更新成功')
    }
    await loadData()
  } catch (err) {
    showError('批量更新失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function installAvailablePlugin(pluginId: string) {
  installingPlugins.value[pluginId] = true
  try {
    const res = await InstallPlugin(pluginId)
    if (res && !res.success) {
      showError('安装失败: ' + res.error)
    } else {
      showSuccess('插件安装成功')
      await loadData()
    }
  } catch (err) {
    showError('安装失败: ' + err)
  } finally {
    installingPlugins.value[pluginId] = false
  }
}

async function submitAddMarketplace() {
  const source = addMarketDialog.value.source.trim()
  if (!source) return
  addMarketDialog.value.submitting = true
  try {
    const res = await AddMarketplace(source)
    if (res && !res.success) {
      showError('添加市场失败: ' + res.error)
    } else {
      showSuccess('市场添加成功')
      addMarketDialog.value.show = false
      addMarketDialog.value.source = ''
      await loadData()
    }
  } catch (err) {
    showError('添加市场失败: ' + err)
  } finally {
    addMarketDialog.value.submitting = false
  }
}

function formatInstallCount(count: number) {
  if (!count) return ''
  if (count >= 1000) return (count / 1000).toFixed(1) + 'k'
  return String(count)
}

async function togglePlugin(p: any) {
  const isEnabled = p.enabled
  const action = isEnabled ? DisablePlugin : EnablePlugin
  p.enabled = !isEnabled // optimistic update
  try {
    const res = await action(p.id)
    if (res && !res.success) {
      p.enabled = isEnabled // rollback
      showError(`操作失败: ` + res.error)
    } else {
      showSuccess(isEnabled ? '已禁用' : '已启用')
    }
  } catch (err) {
    p.enabled = isEnabled // rollback
    showError('操作失败: ' + err)
  }
}

async function updatePlugin(id: string) {
  loading.value = true
  try {
    const res = await UpdatePlugin(id)
    if (res && !res.success) {
      showError('更新失败: ' + res.error)
    } else {
      const output = (res?.output || '').toLowerCase()
      if (output.includes('already at') || output.includes('latest version')) {
        showSuccess('已是最新版本')
      } else {
        showSuccess('插件已更新至新版本')
        await loadData()
      }
    }
  } catch (err) {
    showError('更新失败: ' + err)
  } finally {
    loading.value = false
  }
}

function confirmUninstall(plugin: any) {
  confirmDialog.value = {
    show: true,
    title: '确认卸载',
    message: `确定要卸载插件 "${plugin.name}" 吗？此操作不可恢复。`,
    action: async () => {
      loading.value = true
      try {
        const res = await UninstallPlugin(plugin.id)
        if (res && !res.success) {
          showError('卸载失败: ' + res.error)
        } else {
          showSuccess('插件已卸载')
          await loadData()
        }
      } catch (err) {
        showError('卸载失败: ' + err)
      } finally {
        loading.value = false
        confirmDialog.value.show = false
      }
    }
  }
}

async function toggleDetail(pluginId: string) {
  if (expandedPluginId.value === pluginId) {
    expandedPluginId.value = null
    return
  }

  expandedPluginId.value = pluginId
  if (!pluginDetails.value[pluginId]) {
    loadingDetails.value[pluginId] = true
    try {
      const [detail, subItems, state] = await Promise.all([
        GetPluginDetail(pluginId),
        GetPluginSubItems(pluginId),
        GetPluginSubItemStates(pluginId)
      ])
      pluginDetails.value[pluginId] = {
        ...(detail || {}),
        subItems: mergeSubItemStates(subItems || detail?.subItems || [], state),
      }
      const defaultItem = selectedDetailItem(pluginId, pluginDetails.value[pluginId])
      if (defaultItem) selectedDetailItems.value[pluginId] = defaultItem.key
    } catch (err) {
      showError('加载详情失败: ' + err)
    } finally {
      loadingDetails.value[pluginId] = false
    }
  }
}

async function toggleSubItem(pluginId: string, item: any) {
  const nextEnabled = !item.enabled
  item.enabled = nextEnabled
  try {
    await SetSubItemEnabled(pluginId, { type: item.type, name: item.name }, nextEnabled)
    showSuccess(nextEnabled ? '子项已启用' : '子项已禁用')
  } catch (err) {
    item.enabled = !nextEnabled
    showError('更新子项失败: ' + err)
  }
}

function formatDate(dateStr: string) {
  if (!dateStr) return '-'
  try {
    const d = new Date(dateStr)
    return d.toLocaleString('zh-CN', { 
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit'
    })
  } catch {
    return dateStr
  }
}

onMounted(() => {
  loadData()
})
</script>

<template>
  <div class="plugins-view">
    <!-- Loading bar -->
    <div class="loading-bar" v-if="loading"></div>

    <!-- Toolbar -->
    <div class="toolbar">
      <div class="toolbar-left">
        <h2 class="section-title">已安装插件 ({{ installedPlugins.length }})</h2>
      </div>
      <div class="toolbar-right">
        <button class="btn secondary" @click="loadData" :disabled="loading">
          <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" style="margin-right: 6px;">
            <polyline points="23 4 23 10 17 10"></polyline>
            <polyline points="1 20 1 14 7 14"></polyline>
            <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path>
          </svg>
          刷新
        </button>
      </div>
    </div>

    <!-- Installed Plugins List (grouped by marketplace) -->
    <div class="plugins-list">
      <div class="empty-state card" v-if="installedPlugins.length === 0">
        <svg viewBox="0 0 24 24" width="48" height="48" stroke="#3a4f5e" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round">
          <rect x="4" y="4" width="16" height="16" rx="2" ry="2"></rect>
          <rect x="9" y="9" width="6" height="6"></rect>
          <line x1="9" y1="1" x2="9" y2="4"></line>
          <line x1="15" y1="1" x2="15" y2="4"></line>
          <line x1="9" y1="20" x2="9" y2="23"></line>
          <line x1="15" y1="20" x2="15" y2="23"></line>
          <line x1="20" y1="9" x2="23" y2="9"></line>
          <line x1="20" y1="14" x2="23" y2="14"></line>
          <line x1="1" y1="9" x2="4" y2="9"></line>
          <line x1="1" y1="14" x2="4" y2="14"></line>
        </svg>
        <p class="empty-text">暂未安装任何插件</p>
      </div>

      <div class="installed-group card" v-for="group in installedByMarketplace" :key="group.name">
        <div class="card-header clickable" @click="expandedInstalledGroups[group.name] = !expandedInstalledGroups[group.name]">
          <div class="header-left">
            <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="section-icon">
              <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path>
            </svg>
            <h3 class="card-title">{{ group.name }}</h3>
            <span class="badge market-badge">{{ group.plugins.length }} 个插件</span>
            <span class="badge enabled-count-badge">{{ group.plugins.filter((p: any) => p.enabled).length }} 已启用</span>
          </div>
          <div class="header-right">
            <svg :class="['chevron', { expanded: expandedInstalledGroups[group.name] }]" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="6 9 12 15 18 9"></polyline>
            </svg>
          </div>
        </div>

        <div class="card-body installed-group-body" v-if="expandedInstalledGroups[group.name]">
        <div class="plugin-card" v-for="p in group.plugins" :key="p.id" :class="{ 'plugin-disabled': !p.enabled }">
          <div class="plugin-header">
          <div class="plugin-info-main" @click="toggleDetail(p.id)">
            <div class="plugin-title-row">
              <h3 class="plugin-name">{{ p.name }}</h3>
              <span class="badge" :class="pluginTypeClass(p.pluginType)">{{ pluginTypeLabel(p.pluginType) }}</span>
              <span class="badge version-badge">{{ p.version || 'v1.0.0' }}</span>
              <span class="badge scope-badge" v-if="p.scope">{{ p.scope }}</span>
            </div>
            <p class="plugin-desc">{{ p.manifest?.description || '无描述信息' }}</p>
            <div class="plugin-meta">
              <span class="meta-item">安装于: {{ formatDate(p.installedAt) }}</span>
              <span class="meta-item" v-if="p.lastUpdated">更新于: {{ formatDate(p.lastUpdated) }}</span>
            </div>
          </div>
          
          <div class="plugin-actions-col">
            <div class="status-toggle">
              <span class="toggle-label" :class="{ 'text-enabled': p.enabled, 'text-disabled': !p.enabled }">
                {{ p.enabled ? '已启用' : '已禁用' }}
              </span>
              <button class="ios-toggle" :class="{ active: p.enabled }" @click="togglePlugin(p)"></button>
            </div>
            <div class="action-buttons">
              <button class="btn secondary small" @click="updatePlugin(p.id)" :disabled="loading">更新</button>
              <button class="btn danger small" @click="confirmUninstall(p)" :disabled="loading">卸载</button>
              <button class="btn-icon expand-btn" @click="toggleDetail(p.id)">
                <svg :class="['chevron', { expanded: expandedPluginId === p.id }]" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="6 9 12 15 18 9"></polyline>
                </svg>
              </button>
            </div>
          </div>
        </div>

        <transition name="slide-fade">
          <div class="plugin-detail-panel" v-if="expandedPluginId === p.id">
            <div class="detail-loading" v-if="loadingDetails[p.id]">
              <div class="spinner"></div>
              <span>加载详情中...</span>
            </div>
            <div class="detail-split" v-else-if="pluginDetails[p.id]">
              <aside class="detail-nav" v-if="buildDetailNavItems(pluginDetails[p.id]).length">
                <button
                  type="button"
                  v-for="item in buildDetailNavItems(pluginDetails[p.id])"
                  :key="item.key"
                  class="detail-nav-item"
                  :class="{ active: selectedDetailItem(p.id, pluginDetails[p.id])?.key === item.key }"
                  :aria-label="`查看 ${item.typeLabel} ${item.name} 详情`"
                  :aria-pressed="selectedDetailItem(p.id, pluginDetails[p.id])?.key === item.key"
                  :data-detail-key="item.key"
                  @click.stop="selectDetailItem(p.id, item)"
                >
                  <span class="detail-nav-kind">{{ item.typeLabel }}</span>
                  <span class="detail-nav-name">{{ item.name }}</span>
                  <span v-if="item.badge" class="detail-nav-meta">{{ item.badge }}</span>
                </button>
              </aside>

              <section class="detail-reading-pane" v-if="selectedDetailItem(p.id, pluginDetails[p.id])">
                <div class="detail-pane-header">
                  <span class="badge subitem-kind-badge">{{ selectedDetailItem(p.id, pluginDetails[p.id])?.typeLabel }}</span>
                  <h4 class="detail-pane-title">{{ selectedDetailItem(p.id, pluginDetails[p.id])?.name }}</h4>
                  <div class="detail-item-actions" v-if="selectedDetailItem(p.id, pluginDetails[p.id])?.subItem">
                    <span class="toggle-label" :class="{ 'text-enabled': selectedDetailItem(p.id, pluginDetails[p.id])?.subItem?.enabled, 'text-disabled': !selectedDetailItem(p.id, pluginDetails[p.id])?.subItem?.enabled }">
                      {{ selectedDetailItem(p.id, pluginDetails[p.id])?.subItem?.enabled ? '已启用' : '已禁用' }}
                    </span>
                    <button class="ios-toggle" :class="{ active: selectedDetailItem(p.id, pluginDetails[p.id])?.subItem?.enabled }" @click="toggleSubItem(p.id, selectedDetailItem(p.id, pluginDetails[p.id])?.subItem)"></button>
                  </div>
                </div>
                <p class="detail-pane-desc">{{ selectedDetailItem(p.id, pluginDetails[p.id])?.description || '该条目未声明描述。' }}</p>
                <div class="detail-pane-grid">
                  <div class="detail-kv">
                    <span>类型</span>
                    <strong>{{ selectedDetailItem(p.id, pluginDetails[p.id])?.typeLabel }}</strong>
                  </div>
                  <div class="detail-kv" v-if="selectedDetailItem(p.id, pluginDetails[p.id])?.path">
                    <span>路径</span>
                    <strong class="path-text">{{ selectedDetailItem(p.id, pluginDetails[p.id])?.path }}</strong>
                  </div>
                  <div class="detail-kv" v-if="selectedDetailItem(p.id, pluginDetails[p.id])?.badge">
                    <span>标记</span>
                    <strong>{{ selectedDetailItem(p.id, pluginDetails[p.id])?.badge }}</strong>
                  </div>
                </div>
                <div class="mcp-summary" v-if="selectedMcpServerSummary(p.id, pluginDetails[p.id])">
                  <div class="mcp-summary-title">MCP 安全摘要</div>
                  <div class="detail-pane-grid">
                    <div class="detail-kv">
                      <span>Server</span>
                      <strong>{{ selectedMcpServerSummary(p.id, pluginDetails[p.id])?.name }}</strong>
                    </div>
                    <div class="detail-kv">
                      <span>类型</span>
                      <strong>{{ selectedMcpServerSummary(p.id, pluginDetails[p.id])?.transport }}</strong>
                    </div>
                    <div class="detail-kv">
                      <span>命令</span>
                      <strong>{{ selectedMcpServerSummary(p.id, pluginDetails[p.id])?.command }}</strong>
                    </div>
                    <div class="detail-kv">
                      <span>参数</span>
                      <strong>{{ selectedMcpServerSummary(p.id, pluginDetails[p.id])?.argsCount }} 项，内容已隐藏</strong>
                    </div>
                    <div class="detail-kv">
                      <span>远程端点</span>
                      <strong>{{ selectedMcpServerSummary(p.id, pluginDetails[p.id])?.hasRemoteEndpoint ? '已配置，完整地址已隐藏' : '未声明' }}</strong>
                    </div>
                    <div class="detail-kv">
                      <span>敏感配置</span>
                      <strong>{{ selectedMcpServerSummary(p.id, pluginDetails[p.id])?.hasSensitiveFields || selectedMcpServerSummary(p.id, pluginDetails[p.id])?.hasEnv || selectedMcpServerSummary(p.id, pluginDetails[p.id])?.hasHeaders ? '已检测并隐藏' : '未检测到敏感字段' }}</strong>
                    </div>
                  </div>
                </div>
              </section>

              <div class="empty-state-sm" v-else-if="!hasDetailResources(pluginDetails[p.id])">
                该插件未声明任何可用资源
              </div>
            </div>
          </div>
        </transition>
      </div>
      </div>
      </div>
    </div>

    <!-- Available Plugins + Marketplaces -->
    <div class="available-section">
      <div class="available-toolbar">
        <h2 class="section-title">市场与可安装插件 ({{ filteredAvailableCount }})</h2>
        <div class="available-toolbar-controls">
          <button class="btn secondary small" @click="addMarketDialog.show = true">添加市场</button>
          <button class="btn secondary small" @click="updateAllMarketplaces" :disabled="loading || marketplaces.length === 0">全部更新</button>
          <div class="search-box">
            <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" class="search-icon"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>
            <input type="text" v-model="searchQuery" placeholder="搜索插件名称..." class="search-input" />
            <button v-if="searchQuery" class="search-clear" @click="searchQuery = ''">×</button>
          </div>
          <div class="sort-pills">
            <button class="sort-pill" :class="{ active: sortBy === 'installCount' }" @click="sortBy = 'installCount'">安装量 ↓</button>
            <button class="sort-pill" :class="{ active: sortBy === 'name' }" @click="sortBy = 'name'">名称 A-Z</button>
          </div>
        </div>
      </div>

      <div class="market-console card">
        <aside class="market-source-pane">
          <div class="empty-state compact" v-if="marketplaceConsoleItems.length === 0 && !loading">
            <p class="empty-text">暂无市场源</p>
          </div>
          <button
            v-for="market in marketplaceConsoleItems"
            :key="market.name"
            type="button"
            class="market-source-item"
            :class="{ active: selectedMarketplaceItem?.name === market.name }"
            @click="selectedMarketplace = market.name"
          >
            <span class="market-source-title">{{ market.name }}</span>
            <span class="market-source-meta">{{ market.plugins.length }} 个可安装</span>
            <span class="market-url">{{ market.url || market.repo || market.installLocation || market.source || '-' }}</span>
          </button>
        </aside>
        <section class="market-plugin-pane">
          <div class="pane-toolbar" v-if="selectedMarketplaceItem">
            <div>
              <h3 class="pane-title">{{ selectedMarketplaceItem.name }}</h3>
              <p class="pane-subtitle">{{ selectedAvailablePlugins.length }} 个匹配插件</p>
            </div>
            <button class="btn secondary small" @click="updateMarketplace(selectedMarketplaceItem.name)" :disabled="loading || !selectedMarketplaceItem.name">更新市场</button>
          </div>
          <div class="empty-state compact" v-if="filteredAvailableCount === 0 && searchQuery">
            <p class="empty-text">未找到匹配 "{{ searchQuery }}" 的插件</p>
          </div>
          <div class="empty-state compact" v-else-if="selectedAvailablePlugins.length === 0 && !loading">
            <p class="empty-text">当前市场暂无可安装插件</p>
          </div>
          <div class="available-list" v-else>
            <div class="available-item" v-for="ap in selectedAvailablePlugins" :key="ap.pluginId">
              <div class="available-info">
                <div class="available-title-row">
                  <span class="available-name">{{ ap.name }}</span>
                  <span class="badge install-count-badge" v-if="ap.installCount">{{ formatInstallCount(ap.installCount) }} installs</span>
                </div>
                <p class="available-desc">{{ ap.description || '无描述' }}</p>
              </div>
              <div class="available-actions">
                <button
                  class="btn primary small"
                  @click="installAvailablePlugin(ap.pluginId)"
                  :disabled="loading || installingPlugins[ap.pluginId]"
                >
                  {{ installingPlugins[ap.pluginId] ? '安装中...' : '安装' }}
                </button>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>

    <!-- Add Marketplace Dialog -->
    <transition name="dialog-fade">
      <div class="dialog-overlay" v-if="addMarketDialog.show">
        <div class="dialog">
          <h2 class="dialog-title">添加插件市场</h2>
          <div class="dialog-body">
            <p class="dialog-hint">输入市场源地址 (GitHub 仓库、Git URL 或本地路径)</p>
            <div class="input-group">
              <label class="input-label">市场源</label>
              <input
                class="text-input"
                type="text"
                v-model="addMarketDialog.source"
                placeholder="例: owner/repo 或 https://github.com/user/marketplace.git"
                @keydown.enter="submitAddMarketplace"
                :disabled="addMarketDialog.submitting"
              />
            </div>
            <div class="dialog-examples">
              <p class="example-label">支持的格式:</p>
              <code class="example-code">owner/repo</code>
              <code class="example-code">https://github.com/user/marketplace.git</code>
              <code class="example-code">git@github.com:user/marketplace.git</code>
            </div>
          </div>
          <div class="dialog-actions">
            <button class="btn secondary" @click="addMarketDialog.show = false" :disabled="addMarketDialog.submitting">取消</button>
            <button class="btn primary" @click="submitAddMarketplace" :disabled="addMarketDialog.submitting || !addMarketDialog.source.trim()">
              {{ addMarketDialog.submitting ? '添加中...' : '添加' }}
            </button>
          </div>
        </div>
      </div>
    </transition>

    <!-- Confirm Dialog -->
    <transition name="dialog-fade">
      <div class="dialog-overlay" v-if="confirmDialog.show">
        <div class="dialog">
          <h2 class="dialog-title">{{ confirmDialog.title }}</h2>
          <div class="dialog-body">
            <p>{{ confirmDialog.message }}</p>
          </div>
          <div class="dialog-actions">
            <button class="btn secondary" @click="confirmDialog.show = false" :disabled="loading">取消</button>
            <button class="btn danger" @click="confirmDialog.action" :disabled="loading">
              {{ loading ? '处理中...' : '确认' }}
            </button>
          </div>
        </div>
      </div>
    </transition>

  </div>
</template>

<style scoped>
.plugins-view {
  display: flex;
  flex-direction: column;
  gap: 14px;
  position: relative;
}

/* Loading bar */
.loading-bar {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 2px;
  background: linear-gradient(90deg, transparent, #4fc3f7, transparent);
  animation: loading-slide 1.2s ease-in-out infinite;
  z-index: 10;
}

@keyframes loading-slide {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(100%); }
}

.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.section-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

/* Common Card */
.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  overflow: hidden;
}

/* Marketplace Section */
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: rgba(42, 47, 62, 0.3);
  border-bottom: 1px solid transparent;
}

.card-header.clickable {
  cursor: pointer;
  user-select: none;
  transition: background 0.15s;
}

.card-header.clickable:hover {
  background: rgba(42, 47, 62, 0.6);
}

.marketplace-section:has(.card-body) .card-header {
  border-bottom-color: #2a2f3e;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 12px;
}

.section-icon {
  color: #4fc3f7;
}

.card-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.chevron {
  transition: transform 0.2s;
  color: #8899aa;
}

.chevron.expanded {
  transform: rotate(180deg);
}

.market-list {
  display: flex;
  flex-direction: column;
}

.market-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 20px;
  border-bottom: 1px solid #2a2f3e;
}

.market-item:last-child {
  border-bottom: none;
}

.market-info {
  display: flex;
  align-items: center;
  gap: 12px;
}

.market-name {
  font-weight: 600;
  color: #e0e6ed;
}

.market-url {
  font-size: 13px;
  color: #5a6a7a;
  font-family: monospace;
}

/* Plugins List */
.plugins-list {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.installed-group {
  display: flex;
  flex-direction: column;
}

.installed-group-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px;
}

.enabled-count-badge {
  background: rgba(102, 187, 106, 0.1);
  color: #66bb6a;
  font-size: 12px;
}

.plugin-card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  overflow: hidden;
  transition: border-color 0.2s;
}

.plugin-card:hover {
  border-color: #3a4f5e;
}

.plugin-card.plugin-disabled {
  opacity: 0.7;
  border-color: #222738;
}

.plugin-header {
  display: flex;
  justify-content: space-between;
  padding: 14px 16px;
}

.plugin-info-main {
  flex: 1;
  cursor: pointer;
  padding-right: 20px;
}

.plugin-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.plugin-name {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: #e0e6ed;
}

.plugin-desc {
  margin: 0 0 12px 0;
  font-size: 14px;
  color: #8899aa;
  line-height: 1.5;
}

.plugin-meta {
  display: flex;
  gap: 16px;
}

.meta-item {
  font-size: 12px;
  color: #5a6a7a;
}

.plugin-actions-col {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  justify-content: space-between;
  min-width: 180px;
}

.status-toggle {
  display: flex;
  align-items: center;
  gap: 10px;
}

.toggle-label {
  font-size: 13px;
  font-weight: 600;
}

.text-enabled { color: #66bb6a; }
.text-disabled { color: #5a6a7a; }

.action-buttons {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* Badges */
.badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.2;
}

.version-badge {
  background: rgba(79, 195, 247, 0.1);
  color: #4fc3f7;
  font-family: monospace;
}

.market-badge {
  background: rgba(136, 153, 170, 0.15);
  color: #aab8c5;
}

.source-badge {
  background: rgba(102, 187, 106, 0.1);
  color: #66bb6a;
}

.scope-badge {
  background: rgba(255, 167, 38, 0.1);
  color: #ffa726;
}
.type-integration {
  background: rgba(79, 195, 247, 0.12);
  color: #4fc3f7;
}

.type-hybrid {
  background: rgba(255, 167, 38, 0.12);
  color: #ffa726;
}

.type-skill,
.type-hook,
.type-command,
.type-agent,
.type-mcp,
.type-unknown {
  background: rgba(136, 153, 170, 0.12);
  color: #ccd6e0;
}

.subitem-kind-badge {
  background: rgba(136, 153, 170, 0.12);
  color: #aab8c5;
}

/* Detail Panel */
.plugin-detail-panel {
  border-top: 1px solid #2a2f3e;
  background: #111722;
  padding: 12px;
}

.detail-split {
  display: grid;
  grid-template-columns: minmax(190px, 0.32fr) minmax(0, 1fr);
  gap: 12px;
  min-height: 320px;
  max-height: 420px;
}

.detail-nav,
.detail-reading-pane {
  min-height: 0;
  overflow-y: auto;
  border: 1px solid #263140;
  border-radius: 6px;
  background: #0f1219;
}

.detail-nav {
  display: flex;
  flex-direction: column;
  padding: 6px;
}

.detail-nav-item,
.market-source-item {
  width: 100%;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.detail-nav-item {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr);
  gap: 4px 8px;
  padding: 8px;
  border-radius: 4px;
  position: relative;
  z-index: 1;
  pointer-events: auto;
}

.detail-nav-item > * {
  pointer-events: none;
}

.detail-nav-item:hover,
.detail-nav-item.active,
.market-source-item:hover,
.market-source-item.active {
  background: #182232;
}

.detail-nav-item.active,
.market-source-item.active {
  box-shadow: inset 2px 0 0 #4fc3f7;
}

.detail-nav-kind,
.detail-nav-meta,
.market-source-meta {
  color: #6f8090;
  font-size: 11px;
  text-transform: uppercase;
}

.detail-nav-name,
.market-source-title {
  overflow: hidden;
  color: #d8e0e8;
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-nav-meta {
  grid-column: 2;
  text-transform: none;
}

.detail-reading-pane {
  padding: 14px;
}

.detail-pane-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}

.detail-pane-title,
.pane-title {
  margin: 0;
  color: #e0e6ed;
  font-size: 15px;
  font-weight: 600;
}

.detail-pane-desc {
  margin: 0 0 14px;
  color: #aab8c5;
  font-size: 13px;
  line-height: 1.55;
}

.detail-pane-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
}

.detail-kv {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  border: 1px solid #263140;
  border-radius: 4px;
}

.detail-kv span {
  color: #6f8090;
  font-size: 11px;
}

.detail-kv strong {
  color: #ccd6e0;
  font-size: 12px;
  font-weight: 500;
}

.path-text,
.detail-json {
  word-break: break-all;
  font-family: monospace;
}

.detail-json {
  margin: 12px 0 0;
  padding: 10px;
  overflow: auto;
  border: 1px solid #263140;
  border-radius: 4px;
  color: #aab8c5;
}

.mcp-summary {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid #263140;
}

.mcp-summary-title {
  margin-bottom: 8px;
  color: #8899aa;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.detail-loading {
  display: flex;
  align-items: center;
  gap: 12px;
  color: #8899aa;
  font-size: 14px;
}

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid #2a2f3e;
  border-top-color: #4fc3f7;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.detail-content {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 24px;
}

.detail-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.section-title-sm {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: #e0e6ed;
  display: flex;
  align-items: center;
  gap: 6px;
}

.section-title-sm .icon {
  font-size: 16px;
}

.detail-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.detail-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  background: #0f1219;
  padding: 8px 12px;
  border-radius: 6px;
  border: 1px solid #2a2f3e;
}

.item-name {
  font-size: 13px;
  font-weight: 600;
  color: #4fc3f7;
  min-width: 80px;
}

.item-desc {
  font-size: 13px;
  color: #8899aa;
  line-height: 1.4;
}

.detail-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.tag {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #ccd6e0;
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 13px;
}

/* Empty States */
.empty-state {
  padding: 40px 20px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
}

.empty-state.compact {
  padding: 24px 16px;
}

.empty-text {
  margin: 0;
  font-size: 15px;
  color: #8899aa;
}

.empty-state-sm {
  grid-column: 1 / -1;
  text-align: center;
  padding: 20px;
  color: #5a6a7a;
  font-size: 13px;
  background: #0f1219;
  border-radius: 6px;
  border: 1px dashed #2a2f3e;
}

/* Toggle Switch (reused from Settings.vue) */
.ios-toggle {
  position: relative;
  width: 44px;
  height: 24px;
  background: #2a2f3e;
  border-radius: 24px;
  cursor: pointer;
  transition: background 0.2s ease;
  border: none;
  outline: none;
  flex-shrink: 0;
}

.ios-toggle.active {
  background: #66bb6a;
}

.ios-toggle::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 20px;
  height: 20px;
  background: #fff;
  border-radius: 50%;
  transition: transform 0.2s cubic-bezier(0.25, 0.8, 0.25, 1), background 0.2s;
  box-shadow: 0 2px 4px rgba(0,0,0,0.2);
}

.ios-toggle.active::after {
  transform: translateX(20px);
}

/* Buttons */
.btn {
  padding: 8px 16px;
  border-radius: 6px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  border: none;
  outline: none;
  white-space: nowrap;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn.primary {
  background: #4fc3f7;
  color: #0f1219;
}

.btn.primary:hover:not(:disabled) {
  background: #7bd4f9;
}

.btn.secondary {
  background: transparent;
  color: #e0e6ed;
  border: 1px solid #2a2f3e;
}

.btn.secondary:hover:not(:disabled) {
  border-color: #4fc3f7;
  color: #4fc3f7;
}

.btn.danger {
  background: transparent;
  color: #ef5350;
  border: 1px solid #ef5350;
}

.btn.danger:hover:not(:disabled) {
  background: rgba(239, 83, 80, 0.1);
}

.btn.small {
  padding: 6px 12px;
  font-size: 13px;
  border-radius: 4px;
}

.btn-icon {
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 6px;
  border-radius: 4px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s ease;
  color: #8899aa;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #e0e6ed;
}

.expand-btn {
  background: #0f1219;
  border: 1px solid #2a2f3e;
}

/* Dialog */
.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(15, 18, 25, 0.8);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  backdrop-filter: blur(4px);
}

.dialog {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  padding: 24px;
  width: 100%;
  max-width: 400px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dialog-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #e0e6ed;
}

.dialog-body p {
  margin: 0;
  color: #8899aa;
  font-size: 14px;
  line-height: 1.5;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
}

/* Available Plugins - grouped by marketplace */
.available-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.market-console {
  display: grid;
  grid-template-columns: minmax(220px, 0.32fr) minmax(0, 1fr);
  min-height: 360px;
  max-height: 460px;
}

.market-source-pane,
.market-plugin-pane {
  min-height: 0;
  overflow-y: auto;
}

.market-source-pane {
  padding: 8px;
  border-right: 1px solid #2a2f3e;
  background: #141a25;
}

.market-source-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px;
  border-radius: 4px;
}

.market-plugin-pane {
  display: flex;
  flex-direction: column;
}

.pane-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-bottom: 1px solid #2a2f3e;
}

.pane-subtitle {
  margin: 4px 0 0;
  color: #6f8090;
  font-size: 12px;
}

.available-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

.available-toolbar-controls {
  display: flex;
  align-items: center;
  gap: 12px;
}

.search-box {
  position: relative;
  display: flex;
  align-items: center;
}

.search-icon {
  position: absolute;
  left: 10px;
  color: #5a6a7a;
  pointer-events: none;
}

.search-input {
  width: 220px;
  padding: 7px 30px 7px 32px;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #e0e6ed;
  font-size: 13px;
  font-family: inherit;
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.search-input:focus {
  border-color: #4fc3f7;
  box-shadow: 0 0 0 2px rgba(79, 195, 247, 0.12);
}

.search-input::placeholder {
  color: #5a6a7a;
}

.search-clear {
  position: absolute;
  right: 6px;
  background: none;
  border: none;
  color: #5a6a7a;
  font-size: 16px;
  cursor: pointer;
  padding: 2px 6px;
  border-radius: 4px;
  line-height: 1;
  transition: color 0.15s, background 0.15s;
}

.search-clear:hover {
  color: #e0e6ed;
  background: rgba(255, 255, 255, 0.08);
}

.sort-pills {
  display: flex;
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  padding: 2px;
  gap: 2px;
}

.sort-pill {
  padding: 5px 12px;
  background: transparent;
  border: none;
  border-radius: 4px;
  color: #8899aa;
  font-size: 12px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.15s;
  white-space: nowrap;
}

.sort-pill:hover {
  color: #e0e6ed;
}

.sort-pill.active {
  background: #4fc3f7;
  color: #0f1219;
}

.market-group {
  margin: 0;
}

.available-list {
  display: flex;
  flex-direction: column;
  overflow-y: auto;
}

.available-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 14px;
  border-bottom: 1px solid #2a2f3e;
  gap: 20px;
}

.available-item:last-child {
  border-bottom: none;
}

.available-info {
  flex: 1;
  min-width: 0;
}

.available-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}

.available-name {
  font-weight: 600;
  color: #e0e6ed;
  font-size: 14px;
}

.install-count-badge {
  background: rgba(136, 153, 170, 0.1);
  color: #8899aa;
  font-size: 11px;
}

.available-desc {
  margin: 0;
  font-size: 13px;
  color: #8899aa;
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.available-actions {
  flex-shrink: 0;
}


.detail-section-toggle {
  width: 100%;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.section-toggle-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.subitem-count-badge {
  background: rgba(136, 153, 170, 0.12);
  color: #8899aa;
  font-size: 11px;
}

.detail-item.with-toggle {
  justify-content: space-between;
  align-items: center;
}

.detail-item-copy {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.item-name-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.detail-item.with-toggle .item-name {
  min-width: 0;
}

.detail-item-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.toggle-label {
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}

.text-enabled {
  color: #66bb6a;
}

.text-disabled {
  color: #8899aa;
}

/* Add Marketplace Dialog */
.dialog-hint {
  margin: 0 0 16px 0;
  color: #8899aa;
  font-size: 14px;
  line-height: 1.5;
}

.input-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 16px;
}

.input-label {
  font-size: 13px;
  font-weight: 600;
  color: #ccd6e0;
}

.text-input {
  width: 100%;
  padding: 10px 14px;
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #e0e6ed;
  font-size: 14px;
  font-family: monospace;
  outline: none;
  transition: border-color 0.15s;
  box-sizing: border-box;
}

.text-input:focus {
  border-color: #4fc3f7;
}

.text-input::placeholder {
  color: #5a6a7a;
}

.text-input:disabled {
  opacity: 0.5;
}

.dialog-examples {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.example-label {
  margin: 0;
  font-size: 12px;
  color: #5a6a7a;
}

.example-code {
  font-size: 12px;
  color: #8899aa;
  background: #0f1219;
  padding: 3px 8px;
  border-radius: 4px;
  font-family: monospace;
  display: inline-block;
  width: fit-content;
}

/* Transitions */
.slide-fade-enter-active,
.slide-fade-leave-active {
  transition: all 0.2s ease;
}

.slide-fade-enter-from,
.slide-fade-leave-to {
  opacity: 0;
  transform: translateY(-10px);
}

.dialog-fade-enter-active,
.dialog-fade-leave-active {
  transition: opacity 0.15s ease;
}

.dialog-fade-enter-from,
.dialog-fade-leave-to {
  opacity: 0;
}

@media (max-width: 900px) {
  .detail-split,
  .market-console {
    grid-template-columns: 1fr;
    max-height: none;
  }

  .detail-nav,
  .market-source-pane {
    max-height: 180px;
    border-right: 0;
    border-bottom: 1px solid #2a2f3e;
  }

  .plugin-header,
  .available-item {
    flex-direction: column;
    align-items: stretch;
  }

  .plugin-actions-col {
    align-items: stretch;
    min-width: 0;
  }

  .status-toggle,
  .action-buttons {
    justify-content: flex-end;
  }
}
</style>
