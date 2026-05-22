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
const expandedInstalledGroups = ref<Record<string, boolean>>({})
const expandedPluginId = ref<string | null>(null)
const pluginDetails = ref<Record<string, codexplugin.CodexPluginDetail>>({})
const loadingDetails = ref<Record<string, boolean>>({})
const installingPlugins = ref<Record<string, boolean>>({})
const selectedMarketplace = ref('')
const searchQuery = ref('')
const detailResourceGroups = ['skills', 'agents', 'commands', 'hooks', 'mcp'] as const
const selectedDetailItems = ref<Record<string, string>>({})
const codexPluginFixtureMode = ref(isCodexPluginFixtureMode())

type DetailResourceGroup = typeof detailResourceGroups[number]
type DetailDisplayItem = {
  key: string
  name: string
  description: string
  badge: string
  descriptionKind: 'text' | 'path'
  path?: string
}

type DetailNavItem = DetailDisplayItem & {
  group: DetailResourceGroup
  groupLabel: string
}

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

function summarizeMcpServer(detail: codexplugin.CodexPluginDetail, serverName: string): McpServerSummary {
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

function selectedMcpServerSummary(pluginId: string, detail: codexplugin.CodexPluginDetail) {
  const selected = selectedDetailItem(pluginId, detail)
  if (!selected || selected.group !== 'mcp') return null
  return summarizeMcpServer(detail, selected.name)
}

type CodexPluginServiceBridge = {
  RefreshPlugins?: unknown
  GetPluginDetails?: unknown
}

const fixturePluginId = 'amagi-fixture@browser-validation'
const fixtureReadOnlyMessage = '当前为 Codex 插件浏览器验证 fixture 模式，写操作已禁用。'
const wailsRuntimeMissingMessage = '当前页面需要 Wails 运行时；浏览器验证请使用 fixture 参数：#/extensions/plugins/codex?codexPluginFixture=1'

const fixtureData = {
  marketplaces: [
    {
      name: 'browser-validation',
      source: 'fixture',
      repo: 'amagi-codebox/codex-fixture',
      url: 'fixture://codex-plugin-detail-browser-validation',
      installLocation: '/tmp/amagi-codebox-fixtures/codex-marketplace',
      snapshotPath: '/tmp/amagi-codebox-fixtures/codex-marketplace/snapshot',
      lastUpdated: '2026-05-22T00:00:00Z',
      rawLine: 'fixture://codex-plugin-detail-browser-validation'
    }
  ],
  installed: [
    {
      id: fixturePluginId,
      name: 'amagi fixture plugin',
      marketplace: 'browser-validation',
      version: '1.2.14-fixture',
      enabled: true,
      installPath: '/tmp/amagi-codebox-fixtures/plugins/amagi',
      manifestPath: '/tmp/amagi-codebox-fixtures/plugins/amagi/.codex-plugin/plugin.json',
      installedAt: '2026-05-22T00:00:00Z',
      lastUpdated: '2026-05-22T00:00:00Z',
      source: 'browser-fixture'
    }
  ],
  available: [
    {
      pluginId: 'amagi-extra@browser-validation',
      name: 'amagi-extra-fixture',
      marketplaceName: 'browser-validation',
      version: '0.1.0-fixture',
      description: 'Fixture-only available Codex plugin used to verify marketplace rendering.',
      author: 'Amagi fixture',
      repository: 'https://example.invalid/amagi-extra-fixture',
      snapshotPath: '/tmp/amagi-codebox-fixtures/codex-marketplace/snapshot',
      manifestPath: '/tmp/amagi-codebox-fixtures/codex-marketplace/snapshot/amagi-extra/.codex-plugin/plugin.json'
    }
  ],
  warnings: ['浏览器验证 fixture 模式已启用：数据来自前端内置受控 fixture，不会调用真实 Codex 或 Wails 后端。']
} as codexplugin.CodexPluginsData

const fixtureDetails = {
  [fixturePluginId]: {
    id: fixturePluginId,
    name: 'amagi fixture plugin',
    marketplace: 'browser-validation',
    version: '1.2.14-fixture',
    enabled: true,
    installPath: '/tmp/amagi-codebox-fixtures/plugins/amagi',
    manifestPath: '/tmp/amagi-codebox-fixtures/plugins/amagi/.codex-plugin/plugin.json',
    installedAt: '2026-05-22T00:00:00Z',
    lastUpdated: '2026-05-22T00:00:00Z',
    source: 'browser-fixture',
    manifest: {
      name: 'amagi fixture plugin',
      version: '1.2.14-fixture',
      description: 'Controlled Codex plugin fixture for browser interaction validation.',
      author: { name: 'Amagi fixture' },
      license: 'MIT',
      keywords: ['codex', 'fixture', 'browser-validation'],
      homepage: 'https://example.invalid/amagi-fixture',
      repository: 'https://example.invalid/amagi-fixture'
    },
    skills: [
      { name: 'agent-browser', description: 'Browser automation CLI for interactive UI validation.', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/skills/agent-browser/SKILL.md' },
      { name: 'pdf-to-md', description: 'Convert PDF documents to high quality Markdown.', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/skills/pdf-to-md/SKILL.md' },
      { name: 'design-doc-writing', description: 'Write reviewable technical design documents.', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/skills/design-doc-writing/SKILL.md' }
    ],
    agents: [
      { name: 'baize', description: 'Explorer agent for read-only code research.', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/agents/baize.md' },
      { name: 'luban', description: 'Coder agent for production implementation and self-test.', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/agents/luban.md' }
    ],
    commands: [
      { name: 'github-release', description: 'Execute the GitHub release workflow.', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/commands/github-release.md' },
      { name: 'save-session', description: 'Save project state at the end of a conversation.', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/commands/save-session.md' }
    ],
    hooks: [
      { name: 'load-project-context', event: 'SessionStart', type: 'command', command: 'amagi load project context', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/hooks/hooks.json' },
      { name: 'validate-permission', event: 'PreToolUse', type: 'command', command: 'amagi validate permission', filePath: '/tmp/amagi-codebox-fixtures/plugins/amagi/hooks/hooks.json' }
    ],
    hasMcp: true,
    mcpServers: {
      memory: { command: 'npx', args: ['-y', '@modelcontextprotocol/server-memory'] },
      'web-search-prime': { command: 'uvx', args: ['web-search-prime'] }
    },
    pluginType: 'hybrid'
  }
} as unknown as Record<string, codexplugin.CodexPluginDetail>

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

const marketplaceConsoleItems = computed(() => {
  const byName = new Map(availableByMarketplace.value.map(group => [group.name, group]))
  const items = marketplaces.value.map(marketplace => {
    const name = marketplace.name || marketplace.repo || marketplace.url || 'unknown'
    return {
      ...marketplace,
      name,
      plugins: byName.get(name)?.plugins || []
    }
  })
  for (const group of availableByMarketplace.value) {
    if (!items.some(item => item.name === group.name)) {
      items.push({ name: group.name, source: 'codex', plugins: group.plugins } as any)
    }
  }
  return items.sort((a, b) => a.name.localeCompare(b.name))
})

const selectedMarketplaceItem = computed(() => {
  const items = marketplaceConsoleItems.value
  if (!items.length) return null
  return items.find(item => item.name === selectedMarketplace.value) || items[0]
})

const selectedAvailablePlugins = computed(() => selectedMarketplaceItem.value?.plugins || [])

const installedCount = computed(() => installedPlugins.value.length)
const enabledCount = computed(() => installedPlugins.value.filter(plugin => plugin.enabled).length)
const availableCount = computed(() => availablePlugins.value.length)

function codexPluginFixtureFlagEnabled() {
  const values = [window.location.search, window.location.hash]
  return values.some(value => /(?:[?#&]|^)codexPluginFixture=(?:1|true|yes)(?:[&#]|$)/i.test(value))
}

function getCodexPluginServiceBridge(): CodexPluginServiceBridge | undefined {
  return (window as unknown as { go?: { codexplugin?: { Service?: CodexPluginServiceBridge } } }).go?.codexplugin?.Service
}

function hasCodexPluginWailsBridge() {
  const service = getCodexPluginServiceBridge()
  return typeof service?.RefreshPlugins === 'function' && typeof service?.GetPluginDetails === 'function'
}

function isCodexPluginFixtureMode() {
  return codexPluginFixtureFlagEnabled() && !hasCodexPluginWailsBridge()
}

function applyFixtureData() {
  marketplaces.value = fixtureData.marketplaces
  installedPlugins.value = fixtureData.installed
  refreshWarnings.value = fixtureData.warnings || []
  const installedIds = new Set(installedPlugins.value.map(plugin => plugin.id))
  availablePlugins.value = fixtureData.available.filter(plugin => !installedIds.has(plugin.pluginId))
  pluginDetails.value = { ...fixtureDetails }
  ensureExpandedGroups()
}

function showFixtureReadOnlyError(action: string) {
  showError(`${action}失败: ${fixtureReadOnlyMessage}`)
}

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

async function loadData() {
  loading.value = true
  operationError.value = ''
  try {
    codexPluginFixtureMode.value = isCodexPluginFixtureMode()
    if (codexPluginFixtureMode.value) {
      applyFixtureData()
      return
    }
    if (!hasCodexPluginWailsBridge()) {
      throw new Error(wailsRuntimeMissingMessage)
    }
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
    if (!selectedMarketplace.value) selectedMarketplace.value = group
  }
}

async function submitAddMarketplace() {
  if (codexPluginFixtureMode.value) {
    showFixtureReadOnlyError('添加 Codex 市场')
    return
  }
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
  if (codexPluginFixtureMode.value) {
    showFixtureReadOnlyError(`更新市场 ${name}`)
    return
  }
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
  if (codexPluginFixtureMode.value) {
    showFixtureReadOnlyError('批量更新市场')
    return
  }
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
  if (codexPluginFixtureMode.value) {
    showFixtureReadOnlyError('移除 Codex 市场')
    return
  }
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
  if (codexPluginFixtureMode.value) {
    showFixtureReadOnlyError('安装 Codex 插件')
    return
  }
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
  if (codexPluginFixtureMode.value) {
    showFixtureReadOnlyError('更新启用状态')
    return
  }
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
  if (codexPluginFixtureMode.value) {
    showFixtureReadOnlyError('卸载 Codex 插件')
    return
  }
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
  if (codexPluginFixtureMode.value) {
    const detail = fixtureDetails[pluginId]
    if (detail) {
      pluginDetails.value[pluginId] = detail
      const defaultItem = selectedDetailItem(pluginId, detail)
      if (defaultItem) selectedDetailItems.value[pluginId] = defaultItem.key
    }
    return
  }
  loadingDetails.value[pluginId] = true
  try {
    const detail = await GetPluginDetails(selector(pluginId))
    if (detail) pluginDetails.value[pluginId] = detail
    const defaultItem = selectedDetailItem(pluginId, pluginDetails.value[pluginId])
    if (defaultItem) selectedDetailItems.value[pluginId] = defaultItem.key
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
        descriptionKind: commandOrPath.descriptionKind,
        path: item.filePath
      }
    }
    const copy = buildDescription(item.description, item.filePath)
    return {
      key: item.name || item.filePath || `${group}:${index}`,
      name: item.name || item.filePath || group,
      description: copy.description,
      badge: '',
      descriptionKind: copy.descriptionKind,
      path: item.filePath
    }
  })
}

function detailGroupTitle(group: DetailResourceGroup) {
  return ({ skills: 'Skills', agents: 'Agents', commands: 'Commands', hooks: 'Hooks', mcp: 'MCP Servers' } as Record<string, string>)[group]
}

function detailNavItems(detail: codexplugin.CodexPluginDetail): DetailNavItem[] {
  return detailResourceGroups.flatMap(group => detailItems(detail, group).map(item => ({
    ...item,
    group,
    groupLabel: detailGroupTitle(group),
    key: `${group}:${item.key}`
  })))
}

function selectedDetailItem(pluginId: string, detail: codexplugin.CodexPluginDetail) {
  const items = detailNavItems(detail)
  if (items.length === 0) return null
  const selectedKey = selectedDetailItems.value[pluginId]
  return items.find(item => item.key === selectedKey) || items[0]
}

function selectDetailItem(pluginId: string, item: DetailNavItem) {
  selectedDetailItems.value[pluginId] = item.key
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

    <div class="state-banner fixture" v-if="codexPluginFixtureMode">
      <div>
        <strong>Codex 插件浏览器验证 fixture 模式</strong>
        <p>当前数据来自前端内置受控 fixture，仅用于 Vite/浏览器交互验证；生产 Wails 运行时不会自动启用。</p>
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
                <div class="detail-split" v-else-if="pluginDetails[plugin.id]">
                  <aside class="detail-nav" v-if="detailNavItems(pluginDetails[plugin.id]).length">
                    <button
                      type="button"
                      v-for="item in detailNavItems(pluginDetails[plugin.id])"
                      :key="item.key"
                      class="detail-nav-item"
                      :class="{ active: selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.key === item.key }"
                      :aria-label="`查看 ${item.groupLabel} ${item.name} 详情`"
                      :aria-pressed="selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.key === item.key"
                      :data-detail-key="item.key"
                      @click.stop="selectDetailItem(plugin.id, item)"
                    >
                      <span class="detail-nav-kind">{{ item.groupLabel }}</span>
                      <span class="detail-nav-name">{{ item.name }}</span>
                      <span v-if="item.badge" class="detail-nav-meta">{{ item.badge }}</span>
                    </button>
                  </aside>

                  <section class="detail-reading-pane" v-if="selectedDetailItem(plugin.id, pluginDetails[plugin.id])">
                    <div class="detail-pane-header">
                      <span class="badge subitem-count-badge">{{ selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.groupLabel }}</span>
                      <h4 class="detail-pane-title">{{ selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.name }}</h4>
                    </div>
                    <p class="detail-pane-desc">{{ selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.description || '该条目未声明描述。' }}</p>
                    <div class="detail-pane-grid">
                      <div class="detail-kv">
                        <span>插件状态</span>
                        <strong>{{ pluginDetails[plugin.id].enabled ? 'enabled=true' : 'enabled=false' }}</strong>
                      </div>
                      <div class="detail-kv">
                        <span>类型</span>
                        <strong>{{ selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.groupLabel }}</strong>
                      </div>
                      <div class="detail-kv" v-if="selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.path">
                        <span>路径</span>
                        <strong class="path-text">{{ selectedDetailItem(plugin.id, pluginDetails[plugin.id])?.path }}</strong>
                      </div>
                      <div class="detail-kv" v-if="pluginDetails[plugin.id].manifestPath">
                        <span>Manifest</span>
                        <strong class="path-text">{{ pluginDetails[plugin.id].manifestPath }}</strong>
                      </div>
                    </div>
                    <div class="detail-meta-grid compact-meta">
                      <span v-if="pluginDetails[plugin.id].manifest?.author">作者: {{ formatAuthor(pluginDetails[plugin.id].manifest.author) }}</span>
                      <span v-if="pluginDetails[plugin.id].manifest?.repository">仓库: {{ pluginDetails[plugin.id].manifest.repository }}</span>
                      <span v-if="pluginDetails[plugin.id].manifest?.license">许可: {{ pluginDetails[plugin.id].manifest.license }}</span>
                      <span>插件类型: {{ pluginTypeLabel(pluginDetails[plugin.id].pluginType) }}</span>
                    </div>
                    <div class="mcp-summary" v-if="selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])">
                      <div class="mcp-summary-title">MCP 安全摘要</div>
                      <div class="detail-pane-grid">
                        <div class="detail-kv">
                          <span>Server</span>
                          <strong>{{ selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.name }}</strong>
                        </div>
                        <div class="detail-kv">
                          <span>类型</span>
                          <strong>{{ selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.transport }}</strong>
                        </div>
                        <div class="detail-kv">
                          <span>命令</span>
                          <strong>{{ selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.command }}</strong>
                        </div>
                        <div class="detail-kv">
                          <span>参数</span>
                          <strong>{{ selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.argsCount }} 项，内容已隐藏</strong>
                        </div>
                        <div class="detail-kv">
                          <span>远程端点</span>
                          <strong>{{ selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.hasRemoteEndpoint ? '已配置，完整地址已隐藏' : '未声明' }}</strong>
                        </div>
                        <div class="detail-kv">
                          <span>敏感配置</span>
                          <strong>{{ selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.hasSensitiveFields || selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.hasEnv || selectedMcpServerSummary(plugin.id, pluginDetails[plugin.id])?.hasHeaders ? '已检测并隐藏' : '未检测到敏感字段' }}</strong>
                        </div>
                      </div>
                    </div>
                  </section>

                  <div class="empty-state-sm" v-else-if="!hasDetailResources(pluginDetails[plugin.id])">
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
        <h2 class="section-title">Codex 市场与可安装插件 ({{ availableFiltered.length }} / {{ availableCount }})</h2>
        <div class="available-toolbar-controls">
          <button class="btn secondary small" @click="addMarketDialog.show = true">添加市场</button>
          <button class="btn secondary small" @click="upgradeAllMarketplaces" :disabled="loading || marketplaces.length === 0">全部更新</button>
          <div class="search-box">
            <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" class="search-icon"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>
            <input type="text" v-model="searchQuery" placeholder="搜索 Codex 插件..." class="search-input" />
            <button v-if="searchQuery" class="search-clear" @click="searchQuery = ''">×</button>
          </div>
        </div>
      </div>

      <div class="market-console card">
        <aside class="market-source-pane">
          <div class="empty-state compact" v-if="marketplaceConsoleItems.length === 0 && !loading">
            <p class="empty-text">暂无 Codex 市场源</p>
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
            <span class="market-url">{{ market.url || market.installLocation || market.snapshotPath || market.rawLine || market.source || '-' }}</span>
          </button>
        </aside>
        <section class="market-plugin-pane">
          <div class="pane-toolbar" v-if="selectedMarketplaceItem">
            <div>
              <h3 class="pane-title">{{ selectedMarketplaceItem.name }}</h3>
              <p class="pane-subtitle">{{ selectedAvailablePlugins.length }} 个匹配插件</p>
            </div>
            <div class="market-actions">
              <button class="btn secondary small" @click="upgradeMarketplace(selectedMarketplaceItem.name)" :disabled="loading">更新</button>
              <button class="btn danger small" @click="confirmRemoveMarketplace(selectedMarketplaceItem)" :disabled="loading">删除</button>
            </div>
          </div>
          <div class="empty-state compact" v-if="availableFiltered.length === 0 && !loading">
            <p class="empty-text">{{ searchQuery || selectedMarketplace ? '未找到匹配的可安装插件' : '暂无可安装 Codex 插件。请先添加或更新市场源。' }}</p>
          </div>
          <div class="empty-state compact" v-else-if="selectedAvailablePlugins.length === 0 && !loading">
            <p class="empty-text">当前市场暂无可安装插件</p>
          </div>
          <div class="available-list" v-else>
            <div class="available-item" v-for="plugin in selectedAvailablePlugins" :key="plugin.pluginId">
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
        </section>
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
  gap: 14px;
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
  font-size: 16px;
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

.state-banner.fixture {
  background: rgba(79, 195, 247, 0.08);
  border-color: rgba(79, 195, 247, 0.35);
}

.state-banner strong {
  color: #ef9a9a;
  font-size: 14px;
}

.state-banner.warning strong {
  color: #ffcc80;
}

.state-banner.fixture strong {
  color: #81d4fa;
}

.state-banner p {
  margin: 4px 0 0;
  color: #c7a0a0;
  font-size: 13px;
}

.state-banner.warning p {
  color: #d8be91;
}

.state-banner.fixture p {
  color: #a8cfe0;
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
  gap: 14px;
}

.available-section,
.detail-section {
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
  padding: 12px;
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
  padding: 14px 16px;
}

.plugin-info-main {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.plugin-name {
  margin: 0;
  color: #e0e6ed;
  font-size: 15px;
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
  grid-template-columns: 74px minmax(0, 1fr);
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

.detail-json {
  margin: 12px 0 0;
  padding: 10px;
  overflow: auto;
  border: 1px solid #263140;
  border-radius: 4px;
  color: #aab8c5;
  font-family: monospace;
  word-break: break-all;
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

.compact-meta {
  margin-top: 12px;
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

.available-list {
  overflow-y: auto;
}

.available-name {
  color: #e0e6ed;
  font-size: 14px;
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
