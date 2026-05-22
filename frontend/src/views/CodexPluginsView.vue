<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue'
import { useToast } from '../composables/useToast'
import {
  AddMarketplace,
  GetPluginDetails,
  InstallPlugin,
  RefreshPlugins,
  RemoveMarketplace,
  SetPluginEnabled,
  UninstallPlugin,
  UpgradeMarketplace
} from '../../wailsjs/go/codexplugin/Service'
import type { codexplugin } from '../../wailsjs/go/models'

const { showSuccess, showError } = useToast()

const loading = ref(false)
const operationError = ref('')
const marketplaces = ref<codexplugin.CodexMarketplace[]>([])
const installedPlugins = ref<codexplugin.CodexPlugin[]>([])
const availablePlugins = ref<codexplugin.CodexAvailablePlugin[]>([])
const refreshWarnings = ref<string[]>([])
const marketplacesExpanded = ref(true)
const expandedInstalledGroups = ref<Record<string, boolean>>({})
const expandedAvailableGroups = ref<Record<string, boolean>>({})
const expandedPluginId = ref<string | null>(null)
const pluginDetails = ref<Record<string, codexplugin.CodexPluginDetail>>({})
const loadingDetails = ref<Record<string, boolean>>({})
const installingPlugins = ref<Record<string, boolean>>({})
const selectedMarketplace = ref('')
const searchQuery = ref('')
const detailResourceGroups = ['skills', 'agents', 'commands', 'hooks', 'mcp'] as const
const expandedDetailGroups = ref<Record<string, boolean>>({})

type DetailResourceGroup = typeof detailResourceGroups[number]
type DetailDisplayItem = {
  key: string
  name: string
  description: string
  badge: string
  descriptionKind: 'text' | 'path'
}

const addMarketDialog = ref({
  show: false,
  source: '',
  submitting: false
})

const confirmDialog = ref<{
  show: boolean
  title: string
  message: string
  confirmTone: 'danger' | 'primary'
  action: () => Promise<void>
}>({
  show: false,
  title: '',
  message: '',
  confirmTone: 'danger',
  action: async () => {}
})

const marketplaceOptions = computed(() => marketplaces.value.map(item => item.name).filter(Boolean).sort())

const installedFiltered = computed(() => {
  const selected = selectedMarketplace.value
  return selected ? installedPlugins.value.filter(plugin => plugin.marketplace === selected) : installedPlugins.value
})

const availableFiltered = computed(() => {
  const selected = selectedMarketplace.value
  const query = searchQuery.value.trim().toLowerCase()
  return availablePlugins.value.filter(plugin => {
    if (selected && plugin.marketplaceName !== selected) return false
    if (!query) return true
    const text = [plugin.name, plugin.pluginId, plugin.description, plugin.author, plugin.repository]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return query.split(/\s+/).filter(Boolean).every(token => text.includes(token))
  })
})

const installedByMarketplace = computed(() => groupInstalled(installedFiltered.value))
const availableByMarketplace = computed(() => groupAvailable(availableFiltered.value))

const installedCount = computed(() => installedPlugins.value.length)
const enabledCount = computed(() => installedPlugins.value.filter(plugin => plugin.enabled).length)
const availableCount = computed(() => availablePlugins.value.length)

function groupInstalled(plugins: codexplugin.CodexPlugin[]) {
  const groups: Record<string, { name: string; plugins: codexplugin.CodexPlugin[] }> = {}
  for (const plugin of plugins) {
    const name = plugin.marketplace || 'local'
    if (!groups[name]) groups[name] = { name, plugins: [] }
    groups[name].plugins.push(plugin)
  }
  return Object.values(groups)
    .map(group => ({
      ...group,
      plugins: group.plugins.sort((a, b) => {
        if (a.enabled !== b.enabled) return a.enabled ? -1 : 1
        return (a.name || a.id).localeCompare(b.name || b.id)
      })
    }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

function groupAvailable(plugins: codexplugin.CodexAvailablePlugin[]) {
  const groups: Record<string, { name: string; plugins: codexplugin.CodexAvailablePlugin[] }> = {}
  for (const plugin of plugins) {
    const name = plugin.marketplaceName || 'unknown'
    if (!groups[name]) groups[name] = { name, plugins: [] }
    groups[name].plugins.push(plugin)
  }
  return Object.values(groups)
    .map(group => ({ ...group, plugins: group.plugins.sort((a, b) => (a.name || '').localeCompare(b.name || '')) }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

function selector(pluginId: string) {
  return { pluginId }
}

function pluginTypeLabel(value = 'unknown') {
  return ({ integration: '集成', hybrid: '混合', skill: 'Skill', hook: 'Hook', command: 'Command', agent: 'Agent', mcp: 'MCP', unknown: '未知' } as Record<string, string>)[value] || value
}

function formatDate(value?: string) {
  if (!value) return '-'
  try {
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return date.toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  } catch {
    return value
  }
}

function formatAuthor(author?: string | Record<string, string>) {
  if (!author) return ''
  if (typeof author === 'string') return author
  return author.name || author.email || Object.values(author).filter(Boolean).join(' / ')
}

function getMcpServerNames(detail?: codexplugin.CodexPluginDetail) {
  return Object.keys(detail?.mcpServers || {})
}

function hasDetailResources(detail?: codexplugin.CodexPluginDetail) {
  return Boolean(
    detail?.manifest?.description ||
    detail?.manifestPath ||
    detail?.installPath ||
    detail?.skills?.length ||
    detail?.agents?.length ||
    detail?.commands?.length ||
    detail?.hooks?.length ||
    getMcpServerNames(detail).length
  )
}

function detailGroupKey(pluginId: string, group: string) {
  return `${pluginId}:${group}`
}

function isDetailGroupExpanded(pluginId: string, group: string) {
  return expandedDetailGroups.value[detailGroupKey(pluginId, group)] ?? true
}

function toggleDetailGroup(pluginId: string, group: string) {
  const key = detailGroupKey(pluginId, group)
  expandedDetailGroups.value[key] = !isDetailGroupExpanded(pluginId, group)
}

async function loadData() {
  loading.value = true
  operationError.value = ''
  try {
    const data = await RefreshPlugins()
    marketplaces.value = data?.marketplaces || []
    installedPlugins.value = data?.installed || []
    refreshWarnings.value = data?.warnings || []
    const installedIds = new Set(installedPlugins.value.map(plugin => plugin.id))
    availablePlugins.value = (data?.available || []).filter(plugin => !installedIds.has(plugin.pluginId))
    ensureExpandedGroups()
  } catch (err) {
    const message = '加载 Codex 插件数据失败: ' + err
    operationError.value = message
    showError(message)
  } finally {
    loading.value = false
  }
}

function ensureExpandedGroups() {
  for (const plugin of installedPlugins.value) {
    const group = plugin.marketplace || 'local'
    if (expandedInstalledGroups.value[group] === undefined) expandedInstalledGroups.value[group] = true
  }
  for (const plugin of availablePlugins.value) {
    const group = plugin.marketplaceName || 'unknown'
    if (expandedAvailableGroups.value[group] === undefined) expandedAvailableGroups.value[group] = true
  }
}

async function submitAddMarketplace() {
  const source = addMarketDialog.value.source.trim()
  if (!source) return
  addMarketDialog.value.submitting = true
  try {
    const result = await AddMarketplace({ source })
    if (result && !result.success) {
      showError('添加 Codex 市场失败: ' + (result.error || result.output || '未知错误'))
      return
    }
    showSuccess('Codex 市场已添加')
    addMarketDialog.value.show = false
    addMarketDialog.value.source = ''
    await loadData()
  } catch (err) {
    showError('添加 Codex 市场失败: ' + err)
  } finally {
    addMarketDialog.value.submitting = false
  }
}

async function upgradeMarketplace(name: string) {
  loading.value = true
  try {
    const result = await UpgradeMarketplace(name)
    if (result && !result.success) {
      showError(`更新市场 ${name} 失败: ` + (result.error || result.output || '未知错误'))
      return
    }
    showSuccess(`市场 ${name} 已更新`)
    await loadData()
  } catch (err) {
    showError('更新市场失败: ' + err)
  } finally {
    loading.value = false
  }
}

async function upgradeAllMarketplaces() {
  loading.value = true
  try {
    let failed = 0
    for (const marketplace of marketplaces.value) {
      const result = await UpgradeMarketplace(marketplace.name)
      if (result && !result.success) failed += 1
    }
    if (failed > 0) {
      showError(`${failed} 个 Codex 市场更新失败，请查看日志`)
    } else {
      showSuccess('所有 Codex 市场已更新')
    }
    await loadData()
  } catch (err) {
    showError('批量更新市场失败: ' + err)
  } finally {
    loading.value = false
  }
}

function confirmRemoveMarketplace(marketplace: codexplugin.CodexMarketplace) {
  confirmDialog.value = {
    show: true,
    title: '移除 Codex 市场',
    message: `确定要移除市场 "${marketplace.name}" 吗？已安装插件不会自动卸载，但可安装列表将随之变化。`,
    confirmTone: 'danger',
    action: async () => {
      loading.value = true
      try {
        const result = await RemoveMarketplace(marketplace.name)
        if (result && !result.success) {
          showError('移除市场失败: ' + (result.error || result.output || '未知错误'))
          return
        }
        if (selectedMarketplace.value === marketplace.name) selectedMarketplace.value = ''
        showSuccess('Codex 市场已移除')
        await loadData()
      } catch (err) {
        showError('移除市场失败: ' + err)
      } finally {
        loading.value = false
        confirmDialog.value.show = false
      }
    }
  }
}

async function installAvailablePlugin(plugin: codexplugin.CodexAvailablePlugin) {
  const pluginId = plugin.pluginId
  installingPlugins.value[pluginId] = true
  try {
    const result = await InstallPlugin(selector(pluginId))
    if (result && !result.success) {
      showError('安装 Codex 插件失败: ' + (result.error || result.output || '未知错误'))
      return
    }
    showSuccess(`Codex 插件 ${plugin.name || pluginId} 已安装并启用`)
    await loadData()
  } catch (err) {
    showError('安装 Codex 插件失败: ' + err)
  } finally {
    installingPlugins.value[pluginId] = false
  }
}

async function togglePlugin(plugin: codexplugin.CodexPlugin) {
  const previous = plugin.enabled
  const next = !previous
  plugin.enabled = next
  try {
    const result = await SetPluginEnabled(selector(plugin.id), next)
    if (result && !result.success) {
      plugin.enabled = previous
      showError('更新启用状态失败: ' + (result.error || result.output || '未知错误'))
      return
    }
    if (pluginDetails.value[plugin.id]) pluginDetails.value[plugin.id].enabled = next
    showSuccess(next ? 'Codex 插件已启用' : 'Codex 插件已禁用')
  } catch (err) {
    plugin.enabled = previous
    showError('更新启用状态失败: ' + err)
  }
}

async function updatePluginViaMarketplace(plugin: codexplugin.CodexPlugin) {
  if (!plugin.marketplace) {
    showError('该插件缺少 marketplace 信息，无法更新市场快照')
    return
  }
  await upgradeMarketplace(plugin.marketplace)
}

function confirmUninstall(plugin: codexplugin.CodexPlugin) {
  confirmDialog.value = {
    show: true,
    title: '卸载 Codex 插件',
    message: `确定要卸载插件 "${plugin.name || plugin.id}" 吗？此操作会调用 codex plugin remove 并清理启用状态。`,
    confirmTone: 'danger',
    action: async () => {
      loading.value = true
      try {
        const result = await UninstallPlugin(selector(plugin.id))
        if (result && !result.success) {
          showError('卸载 Codex 插件失败: ' + (result.error || result.output || '未知错误'))
          return
        }
        delete pluginDetails.value[plugin.id]
        if (expandedPluginId.value === plugin.id) expandedPluginId.value = null
        showSuccess('Codex 插件已卸载')
        await loadData()
      } catch (err) {
        showError('卸载 Codex 插件失败: ' + err)
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
  if (pluginDetails.value[pluginId]) return
  loadingDetails.value[pluginId] = true
  try {
    const detail = await GetPluginDetails(selector(pluginId))
    if (detail) pluginDetails.value[pluginId] = detail
  } catch (err) {
    showError('加载 Codex 插件详情失败: ' + err)
  } finally {
    loadingDetails.value[pluginId] = false
  }
}

function buildDescription(primary?: string, fallbackPath?: string) {
  if (primary) return { description: primary, descriptionKind: 'text' as const }
  if (fallbackPath) return { description: fallbackPath, descriptionKind: 'path' as const }
  return { description: '', descriptionKind: 'text' as const }
}

function detailItems(detail: codexplugin.CodexPluginDetail, group: DetailResourceGroup): DetailDisplayItem[] {
  if (group === 'mcp') {
    return getMcpServerNames(detail).map(name => ({ key: name, name, description: 'MCP Server', badge: '', descriptionKind: 'text' }))
  }
  return ((detail[group] || []) as Array<any>).map((item, index) => {
    if (group === 'hooks') {
      const commandOrPath = buildDescription(item.command, item.filePath)
      return {
        key: item.name || `${item.event}:${item.type}:${item.command || item.filePath || ''}`,
        name: item.event && item.name ? `${item.event} / ${item.name}` : item.event || item.name || 'Hook',
        description: commandOrPath.description,
        badge: item.type || '',
        descriptionKind: commandOrPath.descriptionKind
      }
    }
    const copy = buildDescription(item.description, item.filePath)
    return {
      key: item.name || item.filePath || `${group}:${index}`,
      name: item.name || item.filePath || group,
      description: copy.description,
      badge: '',
      descriptionKind: copy.descriptionKind
    }
  })
}

function detailGroupTitle(group: DetailResourceGroup) {
  return ({ skills: 'Skills', agents: 'Agents', commands: 'Commands', hooks: 'Hooks', mcp: 'MCP Servers' } as Record<string, string>)[group]
}

onMounted(() => {
  loadData()
})
</script>

<template>
  <div class="plugins-view codex-plugins-view">
    <div class="loading-bar" v-if="loading"></div>

    <div class="toolbar">
      <div class="toolbar-left">
        <h2 class="section-title">Codex 插件管理</h2>
        <p class="toolbar-subtitle">
          {{ installedCount }} 个已安装，{{ enabledCount }} 个已启用，{{ marketplaces.length }} 个市场源
        </p>
      </div>
      <div class="toolbar-right">
        <select class="market-filter" v-model="selectedMarketplace" :disabled="loading">
          <option value="">全部市场</option>
          <option v-for="name in marketplaceOptions" :key="name" :value="name">{{ name }}</option>
        </select>
        <button class="btn secondary" @click="loadData" :disabled="loading">刷新</button>
      </div>
    </div>

    <div class="state-banner error" v-if="operationError">
      <div>
        <strong>Codex 插件数据加载失败</strong>
        <p>{{ operationError }}</p>
      </div>
      <button class="btn secondary small" @click="loadData" :disabled="loading">重试</button>
    </div>

    <div class="state-banner warning" v-if="refreshWarnings.length > 0 && !operationError">
      <div>
        <strong>Codex 插件数据部分加载</strong>
        <p v-for="warning in refreshWarnings" :key="warning">{{ warning }}</p>
      </div>
      <button class="btn secondary small" @click="loadData" :disabled="loading">重试</button>
    </div>

    <div class="marketplace-section card">
      <div class="card-header clickable" @click="marketplacesExpanded = !marketplacesExpanded">
        <div class="header-left">
          <svg viewBox="0 0 24 24" width="18" height="18" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="section-icon">
            <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path>
            <polyline points="3.27 6.96 12 12.01 20.73 6.96"></polyline>
            <line x1="12" y1="22.08" x2="12" y2="12"></line>
          </svg>
          <h3 class="card-title">Codex 插件市场源 ({{ marketplaces.length }})</h3>
        </div>
        <div class="header-right">
          <button class="btn secondary small" @click.stop="addMarketDialog.show = true" v-if="marketplacesExpanded">添加市场</button>
          <button class="btn primary small" @click.stop="upgradeAllMarketplaces" :disabled="loading || marketplaces.length === 0" v-if="marketplacesExpanded">全部更新</button>
          <svg :class="['chevron', { expanded: marketplacesExpanded }]" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="6 9 12 15 18 9"></polyline>
          </svg>
        </div>
      </div>

      <div class="card-body" v-if="marketplacesExpanded">
        <div class="empty-state compact" v-if="marketplaces.length === 0 && !loading">
          <p class="empty-text">暂无 Codex 插件市场源。添加市场后即可发现可安装插件。</p>
        </div>
        <div class="market-list" v-else>
          <div class="market-item" v-for="marketplace in marketplaces" :key="marketplace.name">
            <div class="market-info">
              <span class="market-name">{{ marketplace.name }}</span>
              <span class="badge source-badge">{{ marketplace.source || marketplace.repo || 'codex' }}</span>
              <span class="market-url">{{ marketplace.url || marketplace.installLocation || marketplace.snapshotPath || marketplace.rawLine || '-' }}</span>
            </div>
            <div class="market-actions">
              <button class="btn secondary small" @click="upgradeMarketplace(marketplace.name)" :disabled="loading">更新</button>
              <button class="btn danger small" @click="confirmRemoveMarketplace(marketplace)" :disabled="loading">删除</button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="plugins-list">
      <div class="empty-state card" v-if="installedFiltered.length === 0 && !loading">
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
        <p class="empty-text">{{ selectedMarketplace ? '当前市场暂无已安装插件' : '暂未安装任何 Codex 插件' }}</p>
      </div>

      <div class="installed-group card" v-for="group in installedByMarketplace" :key="group.name">
        <div class="card-header clickable" @click="expandedInstalledGroups[group.name] = !expandedInstalledGroups[group.name]">
          <div class="header-left">
            <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="section-icon">
              <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path>
            </svg>
            <h3 class="card-title">{{ group.name }}</h3>
            <span class="badge market-badge">{{ group.plugins.length }} 个插件</span>
            <span class="badge enabled-count-badge">{{ group.plugins.filter(plugin => plugin.enabled).length }} 已启用</span>
          </div>
          <div class="header-right">
            <svg :class="['chevron', { expanded: expandedInstalledGroups[group.name] }]" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="6 9 12 15 18 9"></polyline>
            </svg>
          </div>
        </div>

        <div class="card-body installed-group-body" v-if="expandedInstalledGroups[group.name]">
          <div class="plugin-card" v-for="plugin in group.plugins" :key="plugin.id" :class="{ 'plugin-disabled': !plugin.enabled }">
            <div class="plugin-header">
              <div class="plugin-info-main" @click="toggleDetail(plugin.id)">
                <div class="plugin-title-row">
                  <h3 class="plugin-name">{{ plugin.name || plugin.id }}</h3>
                  <span class="badge version-badge">{{ plugin.version || 'version unknown' }}</span>
                  <span class="badge source-badge" v-if="plugin.source">{{ plugin.source }}</span>
                </div>
                <p class="plugin-desc">{{ plugin.id }}</p>
                <div class="plugin-meta">
                  <span class="meta-item">安装路径: {{ plugin.installPath || '-' }}</span>
                  <span class="meta-item" v-if="plugin.lastUpdated">更新于: {{ formatDate(plugin.lastUpdated) }}</span>
                </div>
              </div>

              <div class="plugin-actions-col">
                <div class="status-toggle">
                  <span class="toggle-label" :class="{ 'text-enabled': plugin.enabled, 'text-disabled': !plugin.enabled }">
                    {{ plugin.enabled ? '已启用' : '已禁用' }}
                  </span>
                  <button class="ios-toggle" :class="{ active: plugin.enabled }" :aria-label="plugin.enabled ? '禁用插件' : '启用插件'" @click="togglePlugin(plugin)"></button>
                </div>
                <div class="action-buttons">
                  <button class="btn secondary small" @click="updatePluginViaMarketplace(plugin)" :disabled="loading">更新市场</button>
                  <button class="btn danger small" @click="confirmUninstall(plugin)" :disabled="loading">卸载</button>
                  <button class="btn-icon expand-btn" @click="toggleDetail(plugin.id)" :aria-label="expandedPluginId === plugin.id ? '收起详情' : '查看详情'">
                    <svg :class="['chevron', { expanded: expandedPluginId === plugin.id }]" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="6 9 12 15 18 9"></polyline>
                    </svg>
                  </button>
                </div>
              </div>
            </div>

            <transition name="slide-fade">
              <div class="plugin-detail-panel" v-if="expandedPluginId === plugin.id">
                <div class="detail-loading" v-if="loadingDetails[plugin.id]">
                  <div class="spinner"></div>
                  <span>加载详情中...</span>
                </div>
                <div class="detail-content" v-else-if="pluginDetails[plugin.id]">
                  <div class="detail-section detail-overview">
                    <h4 class="section-title-sm">Manifest 与状态</h4>
                    <div class="detail-item vertical">
                      <span class="item-name">状态</span>
                      <span class="item-desc">{{ pluginDetails[plugin.id].enabled ? 'enabled=true' : 'enabled=false' }}</span>
                    </div>
                    <div class="detail-item vertical">
                      <span class="item-name">描述</span>
                      <span class="item-desc">{{ pluginDetails[plugin.id].manifest?.description || '未声明描述' }}</span>
                    </div>
                    <div class="detail-item vertical">
                      <span class="item-name">Manifest</span>
                      <span class="item-desc path-text">{{ pluginDetails[plugin.id].manifestPath || '未发现 manifest' }}</span>
                    </div>
                    <div class="detail-item vertical">
                      <span class="item-name">安装路径</span>
                      <span class="item-desc path-text">{{ pluginDetails[plugin.id].installPath || '未知' }}</span>
                    </div>
                    <div class="detail-tags" v-if="pluginDetails[plugin.id].manifest?.keywords?.length">
                      <span class="tag" v-for="keyword in pluginDetails[plugin.id].manifest.keywords" :key="keyword">{{ keyword }}</span>
                    </div>
                    <div class="detail-meta-grid">
                      <span v-if="pluginDetails[plugin.id].manifest?.author">作者: {{ formatAuthor(pluginDetails[plugin.id].manifest.author) }}</span>
                      <span v-if="pluginDetails[plugin.id].manifest?.repository">仓库: {{ pluginDetails[plugin.id].manifest.repository }}</span>
                      <span v-if="pluginDetails[plugin.id].manifest?.license">许可: {{ pluginDetails[plugin.id].manifest.license }}</span>
                      <span>类型: {{ pluginTypeLabel(pluginDetails[plugin.id].pluginType) }}</span>
                    </div>
                  </div>

                  <template v-for="groupName in detailResourceGroups" :key="groupName">
                    <div class="detail-section" v-if="detailItems(pluginDetails[plugin.id], groupName).length">
                      <button type="button" class="detail-section-toggle" @click="toggleDetailGroup(plugin.id, groupName)">
                        <span class="section-title-sm">
                          <svg v-if="groupName === 'skills'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="icon"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"></polygon></svg>
                          <svg v-else-if="groupName === 'agents'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="icon"><rect x="3" y="11" width="18" height="10" rx="2"></rect><circle cx="12" cy="5" r="2"></circle><path d="M12 7v4"></path><line x1="8" y1="16" x2="8" y2="16"></line><line x1="16" y1="16" x2="16" y2="16"></line></svg>
                          <svg v-else-if="groupName === 'commands'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="icon"><polyline points="4 17 10 11 4 5"></polyline><line x1="12" y1="19" x2="20" y2="19"></line></svg>
                          <svg v-else-if="groupName === 'hooks'" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="icon"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"></path><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"></path></svg>
                          <svg v-else viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="icon"><rect x="2" y="4" width="20" height="16" rx="2"></rect><line x1="7" y1="8" x2="7" y2="16"></line><line x1="11" y1="8" x2="11" y2="16"></line><line x1="15" y1="8" x2="15" y2="16"></line></svg>
                          {{ detailGroupTitle(groupName) }}
                        </span>
                        <span class="section-toggle-meta">
                          <span class="badge subitem-count-badge">{{ detailItems(pluginDetails[plugin.id], groupName).length }}</span>
                          <svg :class="['chevron', { expanded: isDetailGroupExpanded(plugin.id, groupName) }]" viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                            <polyline points="6 9 12 15 18 9"></polyline>
                          </svg>
                        </span>
                      </button>
                      <div class="detail-list" v-if="isDetailGroupExpanded(plugin.id, groupName)">
                        <div class="detail-item" v-for="item in detailItems(pluginDetails[plugin.id], groupName)" :key="item.key">
                          <div class="detail-item-copy">
                            <span class="item-name-line">
                              <span class="item-name">{{ item.name }}</span>
                              <span class="badge source-badge" v-if="item.badge">{{ item.badge }}</span>
                            </span>
                            <span :class="['item-desc', { 'path-text': item.descriptionKind === 'path' }]" v-if="item.description">{{ item.description }}</span>
                          </div>
                        </div>
                      </div>
                    </div>
                  </template>

                  <div class="empty-state-sm" v-if="!hasDetailResources(pluginDetails[plugin.id])">
                    该 Codex 插件未声明 manifest 或资源清单
                  </div>
                </div>
              </div>
            </transition>
          </div>
        </div>
      </div>
    </div>

    <div class="available-section">
      <div class="available-toolbar">
        <h2 class="section-title">可安装 Codex 插件 ({{ availableFiltered.length }} / {{ availableCount }})</h2>
        <div class="available-toolbar-controls">
          <div class="search-box">
            <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" class="search-icon"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>
            <input type="text" v-model="searchQuery" placeholder="搜索 Codex 插件..." class="search-input" />
            <button v-if="searchQuery" class="search-clear" @click="searchQuery = ''">×</button>
          </div>
        </div>
      </div>

      <div class="empty-state card" v-if="availableFiltered.length === 0 && !loading">
        <p class="empty-text">{{ searchQuery || selectedMarketplace ? '未找到匹配的可安装插件' : '暂无可安装 Codex 插件。请先添加或更新市场源。' }}</p>
      </div>

      <div class="market-group card" v-for="group in availableByMarketplace" :key="group.name">
        <div class="card-header clickable" @click="expandedAvailableGroups[group.name] = !expandedAvailableGroups[group.name]">
          <div class="header-left">
            <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" class="section-icon">
              <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path>
            </svg>
            <h3 class="card-title">{{ group.name }}</h3>
            <span class="badge market-badge">{{ group.plugins.length }} 个插件</span>
          </div>
          <div class="header-right">
            <svg :class="['chevron', { expanded: expandedAvailableGroups[group.name] }]" viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="6 9 12 15 18 9"></polyline>
            </svg>
          </div>
        </div>
        <div class="card-body" v-if="expandedAvailableGroups[group.name]">
          <div class="available-list">
            <div class="available-item" v-for="plugin in group.plugins" :key="plugin.pluginId">
              <div class="available-info">
                <div class="available-title-row">
                  <span class="available-name">{{ plugin.name || plugin.pluginId }}</span>
                  <span class="badge version-badge" v-if="plugin.version">{{ plugin.version }}</span>
                  <span class="badge source-badge" v-if="plugin.author">{{ plugin.author }}</span>
                </div>
                <p class="available-desc">{{ plugin.description || plugin.repository || plugin.manifestPath || '无描述' }}</p>
              </div>
              <div class="available-actions">
                <button class="btn primary small" @click="installAvailablePlugin(plugin)" :disabled="loading || installingPlugins[plugin.pluginId]">
                  {{ installingPlugins[plugin.pluginId] ? '安装中...' : '安装' }}
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <transition name="dialog-fade">
      <div class="dialog-overlay" v-if="addMarketDialog.show">
        <div class="dialog">
          <h2 class="dialog-title">添加 Codex 插件市场</h2>
          <div class="dialog-body">
            <p class="dialog-hint">输入 Codex marketplace 源地址，支持 GitHub 仓库、Git URL 或本地路径。</p>
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
              <code class="example-code">/Users/name/codex-marketplace</code>
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

    <transition name="dialog-fade">
      <div class="dialog-overlay" v-if="confirmDialog.show">
        <div class="dialog">
          <h2 class="dialog-title">{{ confirmDialog.title }}</h2>
          <div class="dialog-body">
            <p>{{ confirmDialog.message }}</p>
          </div>
          <div class="dialog-actions">
            <button class="btn secondary" @click="confirmDialog.show = false" :disabled="loading">取消</button>
            <button :class="['btn', confirmDialog.confirmTone]" @click="confirmDialog.action" :disabled="loading">
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
  gap: 20px;
  position: relative;
}

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

.toolbar,
.available-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.toolbar-left {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.toolbar-right,
.available-toolbar-controls {
  display: flex;
  align-items: center;
  gap: 12px;
}

.toolbar-subtitle {
  margin: 0;
  color: #6f8090;
  font-size: 13px;
}

.section-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #e0e6ed;
}

.card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  overflow: hidden;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
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

.marketplace-section:has(.card-body) .card-header,
.installed-group:has(.card-body) .card-header,
.market-group:has(.card-body) .card-header {
  border-bottom-color: #2a2f3e;
}

.header-left,
.header-right,
.market-actions,
.plugin-title-row,
.plugin-meta,
.status-toggle,
.action-buttons,
.available-title-row,
.section-toggle-meta,
.item-name-line,
.detail-tags {
  display: flex;
  align-items: center;
}

.header-left,
.plugin-title-row,
.available-title-row,
.item-name-line,
.detail-tags {
  gap: 10px;
  flex-wrap: wrap;
}

.header-right,
.market-actions,
.status-toggle,
.action-buttons,
.section-toggle-meta {
  gap: 10px;
}

.section-icon {
  color: #4fc3f7;
  flex-shrink: 0;
}

.card-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #e0e6ed;
}

.chevron {
  transition: transform 0.2s;
  color: #8899aa;
}

.chevron.expanded {
  transform: rotate(180deg);
}

.state-banner {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  padding: 14px 16px;
  border-radius: 8px;
  border: 1px solid #2a2f3e;
}

.state-banner.error {
  background: rgba(239, 83, 80, 0.08);
  border-color: rgba(239, 83, 80, 0.35);
}

.state-banner.warning {
  background: rgba(255, 183, 77, 0.08);
  border-color: rgba(255, 183, 77, 0.35);
}

.state-banner strong {
  color: #ef9a9a;
  font-size: 14px;
}

.state-banner.warning strong {
  color: #ffcc80;
}

.state-banner p {
  margin: 4px 0 0;
  color: #c7a0a0;
  font-size: 13px;
}

.state-banner.warning p {
  color: #d8be91;
}

.market-list,
.plugins-list,
.installed-group,
.available-section,
.available-list,
.detail-list,
.detail-section {
  display: flex;
  flex-direction: column;
}

.plugins-list {
  gap: 24px;
}

.available-section,
.detail-section {
  gap: 12px;
}

.market-item,
.available-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 20px;
  padding: 14px 20px;
  border-bottom: 1px solid #2a2f3e;
}

.market-item:last-child,
.available-item:last-child {
  border-bottom: none;
}

.market-info {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.market-name {
  font-weight: 600;
  color: #e0e6ed;
  flex-shrink: 0;
}

.market-url {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #5a6a7a;
  font-family: monospace;
  font-size: 13px;
}

.installed-group-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
}

.plugin-card {
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  overflow: hidden;
  transition: border-color 0.2s, opacity 0.2s;
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
  gap: 20px;
  padding: 20px;
}

.plugin-info-main {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.plugin-name {
  margin: 0;
  color: #e0e6ed;
  font-size: 18px;
  font-weight: 600;
}

.plugin-desc,
.available-desc {
  margin: 0;
  color: #8899aa;
  font-size: 14px;
  line-height: 1.5;
}

.plugin-desc {
  margin: 0 0 12px;
  font-family: monospace;
}

.plugin-meta {
  gap: 16px;
  flex-wrap: wrap;
}

.meta-item {
  max-width: 520px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #5a6a7a;
  font-size: 12px;
}

.plugin-actions-col {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  justify-content: space-between;
  min-width: 220px;
  gap: 18px;
}

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

.market-badge,
.subitem-count-badge,
.type-skill,
.type-hook,
.type-command,
.type-agent,
.type-mcp,
.type-unknown {
  background: rgba(136, 153, 170, 0.12);
  color: #ccd6e0;
}

.enabled-count-badge,
.source-badge {
  background: rgba(102, 187, 106, 0.1);
  color: #66bb6a;
}

.type-integration {
  background: rgba(79, 195, 247, 0.12);
  color: #4fc3f7;
}

.type-hybrid {
  background: rgba(255, 167, 38, 0.12);
  color: #ffa726;
}

.toggle-label {
  color: #8899aa;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}

.text-enabled { color: #66bb6a; }
.text-disabled { color: #8899aa; }

.ios-toggle {
  position: relative;
  width: 44px;
  height: 24px;
  background: #2a2f3e;
  border: none;
  border-radius: 24px;
  cursor: pointer;
  outline: none;
  transition: background 0.2s ease;
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
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2);
  transition: transform 0.2s cubic-bezier(0.25, 0.8, 0.25, 1), background 0.2s;
}

.ios-toggle.active::after {
  transform: translateX(20px);
}

.plugin-detail-panel {
  border-top: 1px solid #2a2f3e;
  background: rgba(15, 18, 25, 0.3);
  padding: 20px;
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

.detail-overview {
  grid-column: 1 / -1;
}

.section-title-sm {
  margin: 0;
  color: #e0e6ed;
  font-size: 14px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
}

.section-title-sm .icon {
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

.detail-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  background: #0f1219;
  padding: 8px 12px;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
}

.detail-item.vertical {
  flex-direction: column;
  gap: 6px;
}

.detail-item-copy {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 6px;
}

.detail-item-copy .item-name {
  min-width: 0;
}

.item-name {
  min-width: 80px;
  color: #4fc3f7;
  font-size: 13px;
  font-weight: 600;
}

.item-desc {
  color: #8899aa;
  font-size: 13px;
  line-height: 1.4;
}

.path-text {
  word-break: break-all;
  font-family: monospace;
}

.detail-meta-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
  color: #8899aa;
  font-size: 13px;
}

.tag {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  color: #ccd6e0;
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 13px;
}

.empty-state,
.empty-state-sm {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  text-align: center;
}

.empty-state {
  padding: 40px 20px;
}

.empty-state.compact {
  padding: 24px 20px;
}

.empty-state-sm {
  grid-column: 1 / -1;
  padding: 20px;
  color: #5a6a7a;
  background: #0f1219;
  border: 1px dashed #2a2f3e;
  border-radius: 6px;
  font-size: 13px;
}

.empty-text {
  margin: 0;
  color: #8899aa;
  font-size: 15px;
}

.available-info {
  flex: 1;
  min-width: 0;
}

.available-name {
  color: #e0e6ed;
  font-size: 15px;
  font-weight: 600;
}

.available-desc {
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  font-size: 13px;
}

.available-actions {
  flex-shrink: 0;
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

.search-input,
.market-filter,
.text-input {
  background: #0f1219;
  border: 1px solid #2a2f3e;
  border-radius: 6px;
  color: #e0e6ed;
  font-family: inherit;
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.search-input {
  width: 240px;
  padding: 7px 30px 7px 32px;
  font-size: 13px;
}

.market-filter {
  min-width: 160px;
  padding: 8px 12px;
  font-size: 13px;
}

.text-input {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 14px;
  font-family: monospace;
  font-size: 14px;
}

.search-input:focus,
.market-filter:focus,
.text-input:focus {
  border-color: #4fc3f7;
  box-shadow: 0 0 0 2px rgba(79, 195, 247, 0.12);
}

.search-input::placeholder,
.text-input::placeholder {
  color: #5a6a7a;
}

.search-clear {
  position: absolute;
  right: 6px;
  padding: 2px 6px;
  border: none;
  border-radius: 4px;
  background: none;
  color: #5a6a7a;
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
  transition: color 0.15s, background 0.15s;
}

.search-clear:hover {
  color: #e0e6ed;
  background: rgba(255, 255, 255, 0.08);
}

.btn {
  padding: 8px 16px;
  border: none;
  border-radius: 6px;
  outline: none;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.15s ease;
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
  border-radius: 4px;
  font-size: 13px;
}

.btn-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 6px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: #8899aa;
  cursor: pointer;
  transition: all 0.15s ease;
}

.btn-icon:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #e0e6ed;
}

.expand-btn {
  background: #0f1219;
  border: 1px solid #2a2f3e;
}

.dialog-overlay {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 18, 25, 0.8);
  backdrop-filter: blur(4px);
}

.dialog {
  width: 100%;
  max-width: 440px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 24px;
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.dialog-title {
  margin: 0;
  color: #e0e6ed;
  font-size: 18px;
  font-weight: 600;
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

.dialog-hint {
  margin: 0 0 16px;
}

.input-group,
.dialog-examples {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.input-group {
  margin-bottom: 16px;
}

.input-label {
  color: #ccd6e0;
  font-size: 13px;
  font-weight: 600;
}

.example-label {
  color: #5a6a7a;
  font-size: 12px;
}

.example-code {
  display: inline-block;
  width: fit-content;
  padding: 3px 8px;
  background: #0f1219;
  border-radius: 4px;
  color: #8899aa;
  font-family: monospace;
  font-size: 12px;
}

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
  .plugin-header,
  .market-item,
  .available-item {
    flex-direction: column;
    align-items: stretch;
  }

  .plugin-actions-col {
    align-items: stretch;
    min-width: 0;
  }

  .status-toggle,
  .action-buttons,
  .market-actions {
    justify-content: flex-end;
  }
}

@media (max-width: 640px) {
  .toolbar,
  .available-toolbar,
  .toolbar-right,
  .available-toolbar-controls {
    align-items: stretch;
    flex-direction: column;
  }

  .search-input,
  .market-filter {
    width: 100%;
  }
}
</style>
