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
  description?: string
  interface?: {
    capabilities?: string[]
  }
}

export interface PluginDetailView extends PluginDetailInput {
  id?: string
  name?: string
  /** CodexPluginDetail 专有（契约 §5.2：标题=displayName || name，描述=longDescription || shortDescription || description） */
  displayName?: string
  longDescription?: string
  shortDescription?: string
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

// ---------------------------------------------------------------------------
// extensions/ui 共享组件类型（plugin-ui-redesign 契约 §4，R0 组件阶段类型首建）
// ---------------------------------------------------------------------------

/**
 * ui/Badge.vue 的 type prop 联合镜像（Badge.vue 未导出 Props 接口，无法直接引用）。
 * Badge 的 type 联合变化时需同步此处。
 */
export type PluginBadgeType =
  | 'type'
  | 'ver'
  | 'tag'
  | 'scope'
  | 'source'
  | 'dup'
  | 'warning'
  | 'pid'

/** PluginRow / DetailHeader 通用徽章描述：逐字段映射 ui/Badge 的 props */
export interface PluginBadgeDescriptor {
  text: string
  type?: PluginBadgeType
  /** 仅 type='type' 时生效（Badge 语义） */
  color?: string
  variant?: 'muted' | 'mono'
}

/** CountTabs 标签项（契约 §4 #1） */
export interface CountTabItem {
  key: string
  label: string
  count: number
  /** true 时该标签不渲染（如 Pi 零计数资源标签） */
  hidden?: boolean
}

/** DetailHeader 面包屑段（契约 §4 #9）；尾段恒不可点 */
export interface BreadcrumbSegment {
  label: string
  clickable?: boolean
}

/** ActionMenu 菜单项（契约 §4 #11） */
export interface ActionMenuItem {
  key: string
  label: string
  disabled?: boolean
}

/** MetaGrid 标签-值项（契约 §4 #8；ui/MetaGrid.vue 内部接口的面板消费视图） */
export interface MetaItemView {
  label: string
  value: string
  mono?: boolean
}
