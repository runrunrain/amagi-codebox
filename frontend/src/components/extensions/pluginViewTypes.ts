export type SubItemType = 'skill' | 'agent' | 'command' | 'hook' | 'mcp'

export interface PluginSubItemView {
  type: string
  name: string
  description?: string
  filePath?: string
  path?: string
  event?: string
  enabled?: boolean
  globallyEnabled?: boolean
  selectable?: boolean
}

export interface PluginResourceItemView {
  name?: string
  description?: string
  filePath?: string
  event?: string
}

export interface PluginDetailInput {
  subItems?: PluginSubItemView[]
  skills?: PluginResourceItemView[]
  agents?: PluginResourceItemView[]
  commands?: PluginResourceItemView[]
  hooks?: PluginResourceItemView[]
  hasMcp?: boolean
  mcpServers?: Record<string, unknown>
}

export interface PluginManifestView {
  interface?: {
    capabilities?: string[]
  }
}

export interface PluginDetailView extends PluginDetailInput {
  id?: string
  name?: string
  marketplace?: string
  manifest?: PluginManifestView
}

export interface InstalledPluginView extends PluginDetailInput {
  id: string
  pluginId?: string
  name: string
  marketplace?: string
  marketplaceName?: string
  version?: string
  description?: string
  scope?: string
  enabled: boolean
  installPath?: string
  manifestPath?: string
  installedAt?: string
  lastUpdated?: string
  gitCommitSha?: string
  source?: string
  pluginType?: string
  hasMcp?: boolean
  warning?: string
  duplicateOf?: string
}

export interface MarketplaceSourceView {
  Name?: string
  name?: string
  Description?: string
  description?: string
  URL?: string
  url?: string
  repo?: string
  source?: string
  installLocation?: string
  autoUpdate?: boolean
  installCount?: number
}

export interface AvailablePluginView {
  id?: string
  pluginId?: string
  name?: string
  description?: string
  author?: string
  marketplace?: string
  marketplaceName?: string
  installCount?: number
  version?: string
  installed?: boolean
}
