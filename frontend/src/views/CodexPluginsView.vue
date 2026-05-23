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
const selectedInstalledPluginId = ref<string | null>(null)
const pluginDetails = ref<Record<string, codexplugin.CodexPluginDetail>>({})
const loadingDetails = ref<Record<string, boolean>>({})
const installingPlugins = ref<Record<string, boolean>>({})
const selectedMarketplace = ref('')
const searchQuery = ref('')
const detailResourceGroups = ['skills', 'agents', 'commands', 'hooks', 'mcp'] as const
const selectedDetailItems = ref<Record<string, string>>({})
const activePluginMainTab = ref<'installed' | 'marketplace'>('installed')
const selectedResourceFilters = ref<Record<string, DetailResourceFilter>>({})
const codexPluginFixtureMode = ref(isCodexPluginFixtureMode())

type DetailResourceGroup = typeof detailResourceGroups[number]
type DetailResourceFilter = 'all' | DetailResourceGroup
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
const pluginMainTabs = [
  { key: 'installed' as const, label: '已安装插件' },
  { key: 'marketplace' as const, label: '市场可安装插件' }
]
const detailResourceFilterOptions = [
  { key: 'all' as const, label: '全部' },
  { key: 'skills' as const, label: 'Skills' },
  { key: 'agents' as const, label: 'Agents' },
  { key: 'commands' as const, label: 'Commands' },
  { key: 'hooks' as const, label: 'Hooks' },
  { key: 'mcp' as const, label: 'MCP' }
]

function resourceDescription(item: any) {
  const value = item?.description || item?.Description || item?.summary || item?.Summary || item?.shortDescription || item?.short_description
  return typeof value === 'string' && value.trim() ? value.trim() : ''
}

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
      source: 'browser-fixture',
      warning: '检测到重复安装记录，已归并到 amagi-fixture@browser-validation 显示。'
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
  warnings: [
    '浏览器验证 fixture 模式已启用：数据来自前端内置受控 fixture，不会调用真实 Codex 或 Wails 后端。',
    '检测到重复 Codex 插件记录，已归并到 amagi-fixture@browser-validation 显示。'
  ]
} as unknown as codexplugin.CodexPluginsData

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
      interface: {
        displayName: 'Amagi Codex 助手',
        shortDescription: '面向 Codex 的 Amagi Agent 与技能集合。',
        longDescription: '提供 Agent、Skill、Command、Hook 与 MCP 能力摘要，帮助用户在 Codex 中安全安装、审阅和更新 Amagi 插件。',
        developerName: 'Amagi',
        category: 'Productivity',
        capabilities: ['agents', 'skills', 'commands', 'hooks', 'mcp'],
        websiteURL: 'https://example.invalid/amagi-fixture'
      },
      author: { name: 'Amagi fixture' },
      license: 'MIT',
      keywords: ['codex', 'fixture', 'browser-validation'],
      homepage: 'https://example.invalid/amagi-fixture',
      repository: 'https://example.invalid/amagi-fixture'
    },
    displayName: 'Amagi Codex 助手',
    shortDescription: '面向 Codex 的 Amagi Agent 与技能集合。',
    longDescription: '提供 Agent、Skill、Command、Hook 与 MCP 能力摘要，帮助用户在 Codex 中安全安装、审阅和更新 Amagi 插件。',
    warning: '检测到重复安装记录，已归并到 amagi-fixture@browser-validation 显示。',
    duplicateOf: 'amagi-fixture@browser-validation',
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
  return installedPlugins.value
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
const installedPluginList = computed(() => installedByMarketplace.value.flatMap(group => group.plugins))

const selectedInstalledPlugin = computed(() => {
  const plugins = installedPluginList.value
  if (plugins.length === 0) return null
  return plugins.find(plugin => plugin.id === selectedInstalledPluginId.value) || plugins[0]
})

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

function ensureSelectedInstalledPlugin() {
  const plugins = installedPluginList.value
  if (plugins.length === 0) {
    selectedInstalledPluginId.value = null
    return null
  }
  const current = plugins.find(plugin => plugin.id === selectedInstalledPluginId.value)
  if (current) return current.id
  selectedInstalledPluginId.value = plugins[0].id
  return plugins[0].id
}

async function preloadSelectedInstalledPlugin() {
  const pluginId = ensureSelectedInstalledPlugin()
  if (pluginId) await loadPluginDetail(pluginId)
}

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
  ensureInitialSelections()
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

function firstNonEmpty(...values: unknown[]) {
  for (const value of values) {
    if (typeof value === 'string' && value.trim()) return value.trim()
  }
  return ''
}

function codexInterface(detail?: codexplugin.CodexPluginDetail): Record<string, any> {
  const manifest = (detail as any)?.manifest
  const manifestInterface = manifest?.interface || manifest?.Interface
  return manifestInterface && typeof manifestInterface === 'object' ? manifestInterface : {}
}

function codexPluginWarning(plugin?: codexplugin.CodexPlugin | codexplugin.CodexPluginDetail | null) {
  const raw = firstNonEmpty((plugin as any)?.warning, (plugin as any)?.Warning)
  if (!raw) return ''
  const canonical = firstNonEmpty((plugin as any)?.duplicateOf, (plugin as any)?.DuplicateOf)
  const mergedText = /归并|duplicate|重复/i.test(raw) ? raw : `检测到重复安装记录：${raw}`
  const mergeSuffix = /归并|merged/i.test(raw) ? '' : (canonical ? ` 已归并显示到 ${canonical}。` : ' 已归并显示。')
  return `${mergedText}${mergeSuffix}建议如需清理历史重复项，请使用 codex plugin remove 检查并处理；本页面不提供直接删除重复项。`
}

function detailDisplayName(detail?: codexplugin.CodexPluginDetail | null, fallback?: codexplugin.CodexPlugin | null) {
  const iface = codexInterface(detail || undefined)
  return firstNonEmpty(
    (detail as any)?.displayName,
    (detail as any)?.DisplayName,
    iface.displayName,
    iface.DisplayName,
    detail?.manifest?.name,
    detail?.name,
    fallback?.name,
    fallback?.id
  )
}

function detailShortDescription(detail?: codexplugin.CodexPluginDetail | null) {
  const iface = codexInterface(detail || undefined)
  return firstNonEmpty(
    (detail as any)?.shortDescription,
    (detail as any)?.ShortDescription,
    iface.shortDescription,
    iface.ShortDescription,
    detail?.manifest?.description
  )
}

function detailLongDescription(detail?: codexplugin.CodexPluginDetail | null) {
  const iface = codexInterface(detail || undefined)
  return firstNonEmpty(
    (detail as any)?.longDescription,
    (detail as any)?.LongDescription,
    iface.longDescription,
    iface.LongDescription,
    detailShortDescription(detail),
    detail?.manifest?.description
  )
}

function detailDeveloperName(detail?: codexplugin.CodexPluginDetail | null) {
  const iface = codexInterface(detail || undefined)
  return firstNonEmpty(iface.developerName, iface.DeveloperName, formatAuthor(detail?.manifest?.author))
}

function detailCategory(detail?: codexplugin.CodexPluginDetail | null) {
  const iface = codexInterface(detail || undefined)
  return firstNonEmpty(iface.category, iface.Category)
}

function detailCapabilities(detail?: codexplugin.CodexPluginDetail | null): string[] {
  const iface = codexInterface(detail || undefined)
  const raw = iface.capabilities || iface.Capabilities
  return Array.isArray(raw) ? raw.filter(item => typeof item === 'string' && item.trim()).map(item => item.trim()) : []
}

function getMcpServerNames(detail?: codexplugin.CodexPluginDetail) {
  return Object.keys(detail?.mcpServers || {})
}

function hasDetailResources(detail?: codexplugin.CodexPluginDetail) {
  return Boolean(
    detailShortDescription(detail) ||
    detailLongDescription(detail) ||
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
    ensureInitialSelections()
    await preloadSelectedInstalledPlugin()
  } catch (err) {
    const message = '加载 Codex 插件数据失败: ' + err
    operationError.value = message
    showError(message)
  } finally {
    loading.value = false
  }
}

function ensureInitialSelections() {
  ensureSelectedInstalledPlugin()
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
        if (selectedInstalledPluginId.value === plugin.id) selectedInstalledPluginId.value = null
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

async function selectInstalledPlugin(pluginId: string) {
  selectedInstalledPluginId.value = pluginId
  await loadPluginDetail(pluginId)
}

async function loadPluginDetail(pluginId: string) {
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
  return { description: '', descriptionKind: 'text' as const }
}

function detailItems(detail: codexplugin.CodexPluginDetail, group: DetailResourceGroup): DetailDisplayItem[] {
  if (group === 'mcp') {
    return getMcpServerNames(detail).map(name => ({ key: name, name, description: 'MCP Server', badge: '', descriptionKind: 'text' }))
  }
  return ((detail[group] || []) as Array<any>).map((item, index) => {
    if (group === 'hooks') {
      const hookDescription = buildDescription(resourceDescription(item), item.filePath)
      return {
        key: item.name || `${item.event}:${item.type}:${item.command || item.filePath || ''}`,
        name: item.event && item.name ? `${item.event} / ${item.name}` : item.event || item.name || 'Hook',
        description: hookDescription.description,
        badge: item.type || '',
        descriptionKind: hookDescription.descriptionKind,
        path: item.filePath
      }
    }
    const copy = buildDescription(resourceDescription(item), item.filePath)
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

function selectedResourceFilter(pluginId: string): DetailResourceFilter {
  return selectedResourceFilters.value[pluginId] || 'all'
}

function detailNavItemsForFilter(detail: codexplugin.CodexPluginDetail, filter: DetailResourceFilter) {
  const items = detailNavItems(detail)
  return filter === 'all' ? items : items.filter(item => item.group === filter)
}

function filteredDetailNavItems(pluginId: string, detail: codexplugin.CodexPluginDetail) {
  return detailNavItemsForFilter(detail, selectedResourceFilter(pluginId))
}

function detailResourceFilterCount(detail: codexplugin.CodexPluginDetail, filter: DetailResourceFilter) {
  return detailNavItemsForFilter(detail, filter).length
}

function selectedDetailItem(pluginId: string, detail: codexplugin.CodexPluginDetail) {
  const items = filteredDetailNavItems(pluginId, detail)
  if (items.length === 0) return null
  const selectedKey = selectedDetailItems.value[pluginId]
  return items.find(item => item.key === selectedKey) || items[0]
}

function selectResourceFilter(pluginId: string, filter: DetailResourceFilter, detail: codexplugin.CodexPluginDetail) {
  selectedResourceFilters.value[pluginId] = filter
  const items = detailNavItemsForFilter(detail, filter)
  if (items.length > 0) {
    selectedDetailItems.value[pluginId] = items[0].key
  } else {
    delete selectedDetailItems.value[pluginId]
  }
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

    <div class="main-tabs" role="tablist" aria-label="Codex 插件管理主内容切换">
      <button
        v-for="tab in pluginMainTabs"
        :key="tab.key"
        type="button"
        class="main-tab"
        :class="{ active: activePluginMainTab === tab.key }"
        role="tab"
        :aria-selected="activePluginMainTab === tab.key"
        @click="activePluginMainTab = tab.key"
      >
        <span>{{ tab.label }}</span>
        <span class="tab-count">{{ tab.key === 'installed' ? installedFiltered.length : availableFiltered.length }}</span>
      </button>
    </div>

    <div v-if="activePluginMainTab === 'installed'" class="installed-master-detail card">
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
        <p class="empty-text">暂未安装任何 Codex 插件</p>
      </div>

      <template v-else>
        <aside class="installed-plugin-pane">
          <template v-for="group in installedByMarketplace" :key="group.name">
            <div class="installed-group-label">
              <span>{{ group.name }}</span>
              <span>{{ group.plugins.length }} 个</span>
            </div>
            <button
              v-for="plugin in group.plugins"
              :key="plugin.id"
              type="button"
              class="installed-plugin-item"
              :class="{ active: selectedInstalledPlugin?.id === plugin.id, 'plugin-disabled': !plugin.enabled }"
              @click="selectInstalledPlugin(plugin.id)"
            >
              <span class="installed-plugin-title-row">
                <span class="installed-plugin-name">{{ plugin.name || plugin.id }}</span>
                <span class="badge version-badge">{{ plugin.version || 'version unknown' }}</span>
              </span>
              <span class="installed-plugin-desc">{{ plugin.id }}</span>
              <span class="installed-plugin-meta-row">
                <span class="badge source-badge" v-if="plugin.source">{{ plugin.source }}</span>
                <span class="badge warning-badge" v-if="codexPluginWarning(plugin)">重复诊断</span>
                <span class="installed-status" :class="{ enabled: plugin.enabled }">{{ plugin.enabled ? '已启用' : '已禁用' }}</span>
              </span>
              <span class="inline-diagnostic" v-if="codexPluginWarning(plugin)">{{ codexPluginWarning(plugin) }}</span>
            </button>
          </template>
        </aside>

        <section class="installed-detail-pane" v-if="selectedInstalledPlugin">
          <div class="selected-plugin-toolbar">
            <div class="selected-plugin-copy">
              <div class="plugin-title-row">
                <h3 class="plugin-name">{{ detailDisplayName(pluginDetails[selectedInstalledPlugin.id], selectedInstalledPlugin) }}</h3>
                <span class="badge version-badge">{{ selectedInstalledPlugin.version || 'version unknown' }}</span>
                <span class="badge source-badge" v-if="selectedInstalledPlugin.source">{{ selectedInstalledPlugin.source }}</span>
                <span class="badge warning-badge" v-if="codexPluginWarning(pluginDetails[selectedInstalledPlugin.id] || selectedInstalledPlugin)">重复诊断</span>
              </div>
              <p class="plugin-desc primary-copy" v-if="detailShortDescription(pluginDetails[selectedInstalledPlugin.id])">{{ detailShortDescription(pluginDetails[selectedInstalledPlugin.id]) }}</p>
              <p class="plugin-desc long-copy" v-if="detailLongDescription(pluginDetails[selectedInstalledPlugin.id]) && detailLongDescription(pluginDetails[selectedInstalledPlugin.id]) !== detailShortDescription(pluginDetails[selectedInstalledPlugin.id])">{{ detailLongDescription(pluginDetails[selectedInstalledPlugin.id]) }}</p>
              <p class="plugin-id-line">{{ selectedInstalledPlugin.id }}</p>
              <div class="duplicate-diagnostic-card" v-if="codexPluginWarning(pluginDetails[selectedInstalledPlugin.id] || selectedInstalledPlugin)">
                <strong>重复安装诊断</strong>
                <p>{{ codexPluginWarning(pluginDetails[selectedInstalledPlugin.id] || selectedInstalledPlugin) }}</p>
              </div>
              <div class="plugin-meta">
                <span class="meta-item">安装路径: {{ selectedInstalledPlugin.installPath || '-' }}</span>
                <span class="meta-item" v-if="selectedInstalledPlugin.lastUpdated">更新于: {{ formatDate(selectedInstalledPlugin.lastUpdated) }}</span>
                <span class="meta-item" v-if="detailDeveloperName(pluginDetails[selectedInstalledPlugin.id])">开发者: {{ detailDeveloperName(pluginDetails[selectedInstalledPlugin.id]) }}</span>
                <span class="meta-item" v-if="detailCategory(pluginDetails[selectedInstalledPlugin.id])">分类: {{ detailCategory(pluginDetails[selectedInstalledPlugin.id]) }}</span>
              </div>
              <div class="capability-row" v-if="detailCapabilities(pluginDetails[selectedInstalledPlugin.id]).length">
                <span class="badge capability-badge" v-for="capability in detailCapabilities(pluginDetails[selectedInstalledPlugin.id])" :key="capability">{{ capability }}</span>
              </div>
            </div>

            <div class="plugin-actions-col selected-actions">
              <div class="status-toggle">
                <span class="toggle-label" :class="{ 'text-enabled': selectedInstalledPlugin.enabled, 'text-disabled': !selectedInstalledPlugin.enabled }">
                  {{ selectedInstalledPlugin.enabled ? '已启用' : '已禁用' }}
                </span>
                <button class="ios-toggle" :class="{ active: selectedInstalledPlugin.enabled }" :aria-label="selectedInstalledPlugin.enabled ? '禁用插件' : '启用插件'" @click="togglePlugin(selectedInstalledPlugin)"></button>
              </div>
              <div class="action-buttons">
                <button class="btn secondary small" @click="updatePluginViaMarketplace(selectedInstalledPlugin)" :disabled="loading">更新市场</button>
                <button class="btn danger small" @click="confirmUninstall(selectedInstalledPlugin)" :disabled="loading">卸载</button>
              </div>
            </div>
          </div>

          <div class="detail-loading" v-if="loadingDetails[selectedInstalledPlugin.id]">
            <div class="spinner"></div>
            <span>加载详情中...</span>
          </div>
          <div class="detail-split" v-else-if="pluginDetails[selectedInstalledPlugin.id]">
            <div class="resource-filter-bar" v-if="detailNavItems(pluginDetails[selectedInstalledPlugin.id]).length">
              <button
                v-for="filter in detailResourceFilterOptions"
                :key="filter.key"
                type="button"
                class="resource-filter-chip"
                :class="{ active: selectedResourceFilter(selectedInstalledPlugin.id) === filter.key }"
                :aria-pressed="selectedResourceFilter(selectedInstalledPlugin.id) === filter.key"
                @click="selectResourceFilter(selectedInstalledPlugin.id, filter.key, pluginDetails[selectedInstalledPlugin.id])"
              >
                <span>{{ filter.label }}</span>
                <span class="filter-count">{{ detailResourceFilterCount(pluginDetails[selectedInstalledPlugin.id], filter.key) }}</span>
              </button>
            </div>
            <aside class="detail-nav" v-if="filteredDetailNavItems(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id]).length">
              <button
                type="button"
                v-for="item in filteredDetailNavItems(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])"
                :key="item.key"
                class="detail-nav-item"
                :class="[{ active: selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.key === item.key }, `kind-${item.group}`]"
                :aria-label="`查看 ${item.groupLabel} ${item.name} 详情`"
                :aria-pressed="selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.key === item.key"
                :data-detail-key="item.key"
                @click.stop="selectDetailItem(selectedInstalledPlugin.id, item)"
              >
                <span class="detail-nav-kind">{{ item.groupLabel }}</span>
                <span class="detail-nav-name">{{ item.name }}</span>
                <span v-if="item.badge" class="detail-nav-meta">{{ item.badge }}</span>
              </button>
            </aside>

            <div class="empty-state-sm resource-empty" v-else-if="detailNavItems(pluginDetails[selectedInstalledPlugin.id]).length">
              当前筛选下暂无资源
            </div>

            <section class="detail-reading-pane" v-if="selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])">
              <div class="detail-pane-header">
                <span class="badge subitem-count-badge">{{ selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.groupLabel }}</span>
                <h4 class="detail-pane-title">{{ selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.name }}</h4>
              </div>
              <div class="description-callout" :class="{ empty: !selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.description || selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.group === 'mcp' }">
                <span class="description-label">说明</span>
                <p>{{ selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.group === 'mcp' ? '暂无说明' : (selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.description || '暂无说明') }}</p>
              </div>
              <div class="detail-pane-grid">
                <div class="detail-kv">
                  <span>插件状态</span>
                  <strong>{{ pluginDetails[selectedInstalledPlugin.id].enabled ? 'enabled=true' : 'enabled=false' }}</strong>
                </div>
                <div class="detail-kv">
                  <span>类型</span>
                  <strong>{{ selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.groupLabel }}</strong>
                </div>
                <div class="detail-kv" v-if="selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.path">
                  <span>路径</span>
                  <strong class="path-text">{{ selectedDetailItem(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.path }}</strong>
                </div>
                <div class="detail-kv" v-if="pluginDetails[selectedInstalledPlugin.id].manifestPath">
                  <span>Manifest</span>
                  <strong class="path-text">{{ pluginDetails[selectedInstalledPlugin.id].manifestPath }}</strong>
                </div>
                <div class="detail-kv" v-if="detailDisplayName(pluginDetails[selectedInstalledPlugin.id], selectedInstalledPlugin)">
                  <span>展示名称</span>
                  <strong>{{ detailDisplayName(pluginDetails[selectedInstalledPlugin.id], selectedInstalledPlugin) }}</strong>
                </div>
              </div>
              <div class="detail-meta-grid compact-meta">
                <span v-if="detailDeveloperName(pluginDetails[selectedInstalledPlugin.id])">作者: {{ detailDeveloperName(pluginDetails[selectedInstalledPlugin.id]) }}</span>
                <span v-if="pluginDetails[selectedInstalledPlugin.id].manifest?.repository">仓库: {{ pluginDetails[selectedInstalledPlugin.id].manifest.repository }}</span>
                <span v-if="pluginDetails[selectedInstalledPlugin.id].manifest?.license">许可: {{ pluginDetails[selectedInstalledPlugin.id].manifest.license }}</span>
                <span v-if="detailCategory(pluginDetails[selectedInstalledPlugin.id])">分类: {{ detailCategory(pluginDetails[selectedInstalledPlugin.id]) }}</span>
                <span>插件类型: {{ pluginTypeLabel(pluginDetails[selectedInstalledPlugin.id].pluginType) }}</span>
              </div>
              <div class="mcp-summary" v-if="selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])">
                <div class="mcp-summary-title">MCP 安全摘要</div>
                <div class="detail-pane-grid">
                  <div class="detail-kv">
                    <span>Server</span>
                    <strong>{{ selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.name }}</strong>
                  </div>
                  <div class="detail-kv">
                    <span>类型</span>
                    <strong>{{ selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.transport }}</strong>
                  </div>
                  <div class="detail-kv">
                    <span>命令</span>
                    <strong>{{ selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.command }}</strong>
                  </div>
                  <div class="detail-kv">
                    <span>参数</span>
                    <strong>{{ selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.argsCount }} 项，内容已隐藏</strong>
                  </div>
                  <div class="detail-kv">
                    <span>远程端点</span>
                    <strong>{{ selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.hasRemoteEndpoint ? '已配置，完整地址已隐藏' : '未声明' }}</strong>
                  </div>
                  <div class="detail-kv">
                    <span>敏感配置</span>
                    <strong>{{ selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.hasSensitiveFields || selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.hasEnv || selectedMcpServerSummary(selectedInstalledPlugin.id, pluginDetails[selectedInstalledPlugin.id])?.hasHeaders ? '已检测并隐藏' : '未检测到敏感字段' }}</strong>
                  </div>
                </div>
              </div>
            </section>

            <div class="empty-state-sm" v-else-if="!hasDetailResources(pluginDetails[selectedInstalledPlugin.id])">
              该 Codex 插件未声明 manifest 或资源清单
            </div>
          </div>
        </section>
      </template>
    </div>

    <div v-if="activePluginMainTab === 'marketplace'" class="available-section">
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

.main-tabs {
  display: inline-flex;
  width: fit-content;
  padding: 3px;
  gap: 3px;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  background: #101722;
}

.main-tab,
.resource-filter-chip {
  display: inline-flex;
  align-items: center;
  border: 0;
  color: #8899aa;
  cursor: pointer;
  font-family: inherit;
  font-weight: 700;
  transition: color 0.15s, background 0.15s, box-shadow 0.15s;
}

.main-tab {
  gap: 8px;
  padding: 8px 14px;
  border-radius: 6px;
  background: transparent;
  font-size: 13px;
}

.main-tab:hover,
.main-tab.active {
  color: #d8e0e8;
  background: #182232;
}

.main-tab.active {
  box-shadow: inset 0 -2px 0 #4fc3f7;
}

.tab-count,
.filter-count {
  min-width: 20px;
  padding: 1px 6px;
  border-radius: 999px;
  background: rgba(136, 153, 170, 0.14);
  color: #aab8c5;
  font-size: 11px;
  line-height: 1.5;
  text-align: center;
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
  height: clamp(520px, 62vh, 760px);
  min-height: 0;
  overflow: hidden;
}

.market-source-pane,
.market-plugin-pane {
  min-height: 0;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
}

.market-source-pane {
  padding: 8px;
  border-right: 1px solid #2a2f3e;
  background: #141a25;
  overflow-y: auto;
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
  overflow: hidden;
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

.installed-master-detail {
  display: grid;
  grid-template-columns: minmax(260px, 300px) minmax(0, 1fr);
  height: clamp(640px, 74vh, 920px);
  min-height: 640px;
  overflow: hidden;
}

.installed-master-detail > .empty-state {
  grid-column: 1 / -1;
  align-self: center;
  border: 0;
}

.installed-plugin-pane,
.installed-detail-pane {
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
}

.installed-plugin-pane {
  padding: 8px;
  border-right: 1px solid #2a2f3e;
  background: #141a25;
}

.installed-detail-pane {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px;
  overflow-x: hidden;
}

.installed-group-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin: 10px 0 6px;
  padding: 8px 10px 8px 12px;
  border-top: 1px solid rgba(79, 195, 247, 0.28);
  border-left: 3px solid #4fc3f7;
  border-radius: 6px;
  background: linear-gradient(90deg, rgba(79, 195, 247, 0.12), rgba(20, 26, 37, 0.88));
  color: #c4d4e2;
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.installed-group-label:first-child {
  margin-top: 0;
}

.installed-group-label span:first-child {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.installed-group-label span:last-child {
  flex-shrink: 0;
  color: #81d4fa;
  font-size: 10px;
}

.installed-plugin-item {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 7px;
  padding: 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.installed-plugin-item:hover,
.installed-plugin-item.active {
  background: #182232;
}

.installed-plugin-item.active {
  box-shadow: inset 2px 0 0 #4fc3f7;
}

.installed-plugin-item.plugin-disabled {
  opacity: 0.68;
}

.installed-plugin-title-row,
.installed-plugin-meta-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.installed-plugin-name {
  min-width: 0;
  overflow: hidden;
  color: #d8e0e8;
  font-size: 13px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.installed-plugin-desc {
  overflow: hidden;
  color: #8899aa;
  font-family: monospace;
  font-size: 12px;
  line-height: 1.4;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.inline-diagnostic {
  display: block;
  overflow: hidden;
  color: #d8be91;
  font-size: 12px;
  line-height: 1.45;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.installed-status {
  margin-left: auto;
  color: #8899aa;
  font-size: 12px;
  font-weight: 700;
}

.installed-status.enabled {
  color: #66bb6a;
}

.selected-plugin-toolbar {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  padding: 12px 14px;
  border: 1px solid #263140;
  border-radius: 8px;
  background: #111722;
}

.selected-plugin-copy {
  min-width: 0;
}

.selected-actions {
  min-width: 220px;
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

.plugin-desc.primary-copy,
.plugin-desc.long-copy {
  font-family: inherit;
}

.plugin-desc.primary-copy {
  margin-bottom: 6px;
  color: #c9d7e2;
  font-size: 14px;
}

.plugin-desc.long-copy {
  max-width: 780px;
  margin-bottom: 10px;
  color: #9fb0be;
  font-size: 13px;
}

.plugin-id-line {
  margin: 0 0 10px;
  color: #5a6a7a;
  font-family: monospace;
  font-size: 12px;
}

.duplicate-diagnostic-card {
  max-width: 780px;
  margin: 0 0 12px;
  padding: 10px 12px;
  border: 1px solid rgba(255, 183, 77, 0.3);
  border-left: 3px solid #ffb74d;
  border-radius: 6px;
  background: rgba(255, 183, 77, 0.07);
}

.duplicate-diagnostic-card strong {
  display: block;
  margin-bottom: 4px;
  color: #ffcc80;
  font-size: 12px;
}

.duplicate-diagnostic-card p {
  margin: 0;
  color: #d8be91;
  font-size: 13px;
  line-height: 1.5;
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

.warning-badge {
  background: rgba(255, 183, 77, 0.12);
  color: #ffcc80;
}

.capability-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}

.capability-badge {
  background: rgba(129, 212, 250, 0.1);
  color: #81d4fa;
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
  grid-template-columns: minmax(180px, 220px) minmax(0, 1fr);
  grid-template-rows: auto minmax(0, 1fr);
  gap: 10px;
  height: clamp(440px, 58vh, 760px);
  min-height: 440px;
  overflow: hidden;
  flex: 1 1 auto;
}

.resource-filter-bar {
  grid-column: 1 / -1;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 8px;
  border: 1px solid #263140;
  border-radius: 6px;
  background: #0f1219;
}

.resource-filter-chip {
  gap: 6px;
  padding: 6px 10px;
  border-radius: 999px;
  background: #141a25;
  font-size: 12px;
}

.resource-filter-chip:hover,
.resource-filter-chip.active {
  color: #e0e6ed;
  background: #1d2a3b;
}

.resource-filter-chip.active {
  box-shadow: inset 0 0 0 1px rgba(79, 195, 247, 0.55);
}

.detail-nav,
.detail-reading-pane {
  height: 100%;
  min-height: 0;
  box-sizing: border-box;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
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
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 5px;
  min-width: 0;
  padding: 9px 10px;
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
  text-transform: uppercase;
}

.detail-nav-kind {
  align-self: flex-start;
  max-width: 100%;
  padding: 2px 7px;
  border: 1px solid rgba(136, 153, 170, 0.18);
  border-radius: 999px;
  background: rgba(136, 153, 170, 0.08);
  font-size: 9px;
  font-weight: 900;
  letter-spacing: 0.08em;
  line-height: 1.3;
}

.detail-nav-meta,
.market-source-meta {
  font-size: 11px;
}

.detail-nav-item.kind-skill .detail-nav-kind,
.detail-nav-item.kind-skills .detail-nav-kind {
  border-color: rgba(77, 208, 225, 0.32);
  background: rgba(77, 208, 225, 0.12);
  color: #80deea;
}

.detail-nav-item.kind-agent .detail-nav-kind,
.detail-nav-item.kind-agents .detail-nav-kind {
  border-color: rgba(179, 157, 219, 0.34);
  background: rgba(179, 157, 219, 0.12);
  color: #d1c4e9;
}

.detail-nav-item.kind-command .detail-nav-kind,
.detail-nav-item.kind-commands .detail-nav-kind {
  border-color: rgba(255, 204, 128, 0.34);
  background: rgba(255, 204, 128, 0.12);
  color: #ffcc80;
}

.detail-nav-item.kind-hook .detail-nav-kind,
.detail-nav-item.kind-hooks .detail-nav-kind {
  border-color: rgba(239, 154, 154, 0.34);
  background: rgba(239, 154, 154, 0.12);
  color: #ef9a9a;
}

.detail-nav-item.kind-mcp .detail-nav-kind {
  border-color: rgba(129, 199, 132, 0.34);
  background: rgba(129, 199, 132, 0.12);
  color: #a5d6a7;
}

.detail-nav-name,
.market-source-title {
  overflow: hidden;
  color: #d8e0e8;
  font-size: 13px;
  font-weight: 600;
}

.market-source-title {
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-nav-name {
  line-height: 1.35;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.detail-nav-meta {
  align-self: flex-start;
  text-transform: none;
}

.detail-reading-pane {
  padding: 14px;
  overflow-x: hidden;
  -webkit-overflow-scrolling: touch;
}

.resource-empty {
  grid-column: 1;
  min-height: 100%;
  justify-content: center;
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

.description-callout {
  margin: 0 0 14px;
  padding: 12px 14px;
  border: 1px solid rgba(79, 195, 247, 0.25);
  border-left: 3px solid #4fc3f7;
  border-radius: 6px;
  background: rgba(79, 195, 247, 0.07);
}

.description-callout.empty {
  border-color: #263140;
  border-left-color: #5a6a7a;
  background: #101722;
}

.description-label {
  display: block;
  margin-bottom: 6px;
  color: #81d4fa;
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.description-callout.empty .description-label {
  color: #6f8090;
}

.description-callout p {
  margin: 0;
  color: #c9d7e2;
  font-size: 13px;
  line-height: 1.6;
}

.description-callout.empty p {
  color: #8899aa;
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
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
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
  .installed-master-detail {
    grid-template-columns: 1fr;
    grid-template-rows: minmax(180px, 0.34fr) minmax(0, 1fr);
    height: clamp(680px, 82vh, 920px);
  }

  .installed-plugin-pane {
    border-right: 0;
    border-bottom: 1px solid #2a2f3e;
  }

  .selected-plugin-toolbar {
    flex-direction: column;
  }

  .detail-split {
    grid-template-columns: 1fr;
    grid-template-rows: auto minmax(120px, 160px) minmax(300px, 1fr);
    height: auto;
    min-height: 490px;
    overflow: visible;
  }

  .market-console {
    grid-template-columns: 1fr;
    grid-template-rows: minmax(180px, 0.34fr) minmax(0, 1fr);
    height: clamp(620px, 78vh, 860px);
  }

  .detail-nav {
    height: auto;
    min-height: 120px;
    max-height: 160px;
    border-right: 0;
    border-bottom: 1px solid #2a2f3e;
  }

  .market-source-pane {
    max-height: 160px;
    border-right: 0;
    border-bottom: 1px solid #2a2f3e;
  }

  .detail-reading-pane {
    height: auto;
    min-height: 300px;
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
