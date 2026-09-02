/**
 * PathPickerModel — 快捷功能「输入工作路径」目录选择器的纯逻辑模型。
 *
 * 与渲染解耦（先例：components/provider/piConcurrency.ts）：不依赖 Vue /
 * DOM，加载器经构造注入（组件传 api/paths.listDirectories，测试传桩），
 * node 环境可直测。组件只做渲染接线（PathPickerDialog.vue）。
 *
 * 覆盖面：目录列表状态、勾选集合有序管理（chips 同序）、面包屑祖先链、
 * 展开下一层（懒加载 + 缓存 + 行内错误重试）、初始 root = dirname(workDir)
 * 与首次失败降级空串（后端 home 兜底）的决策、确认禁用/启用。
 */
import type { DirectoryEntry, ListDirectoriesResult } from '../../api/paths'

/** 目录加载器（组件注入 api/paths.listDirectories；测试注入桩）。 */
export type PathLoader = (root: string) => Promise<ListDirectoriesResult>

/** 展平后的行：目录行 / 子层加载中 / 子层加载失败（行内重试）。 */
export type PathPickerRow =
  | { kind: 'dir'; name: string; path: string; depth: number; expanded: boolean }
  | { kind: 'loading'; depth: number; key: string }
  | { kind: 'error'; depth: number; path: string; message: string }

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/**
 * workDir 的父目录（兼容 / 与 \ 分隔、容忍尾部重复分隔符）。
 * 无父级（根、相对单段、空串）返回空串——交由后端 home 兜底。
 */
export function parentDirOf(p: string): string {
  if (!p) return ''
  const trimmed = p.replace(/[\\/]+$/, '')
  if (!trimmed) return '' // 输入本身是根
  const lastSep = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
  if (lastSep < 0) return '' // 相对单段
  if (lastSep === 0) return trimmed[0] // '/x' 的父是 '/'
  return trimmed.slice(0, lastSep)
}

/** 选择器打开时的初始 root：workDir 的父目录；workDir 为空/无父级返回空串。 */
export function initialRootFor(workDir: string): string {
  return parentDirOf(workDir)
}

/**
 * 面包屑祖先链：当前 root 绝对路径逐段推导（完整、确定性，与导航历史
 * 无关）。POSIX：/a/b/c → /、a、b、c；Windows：C:\Users\bob → C:、Users、bob。
 * 末项即当前层。
 */
export function splitAncestors(root: string): { name: string; path: string }[] {
  const out: { name: string; path: string }[] = []
  let start = 0
  for (let i = 0; i < root.length; i++) {
    const ch = root[i]
    if (ch !== '/' && ch !== '\\') continue
    const prefix = root.slice(0, i + 1)
    const name = root.slice(start, i)
    // 文件系统根（POSIX '/'；Windows 盘符根 'C:\'）：首段直接以根形式入链
    if (!name || (out.length === 0 && /^[A-Za-z]:$/.test(name))) {
      out.push({ name: prefix, path: prefix })
    } else {
      out.push({ name, path: prefix.slice(0, -1) })
    }
    start = i + 1
  }
  if (start < root.length) {
    out.push({ name: root.slice(start), path: root })
  }
  return out.length ? out : [{ name: root, path: root }]
}

/** chip 展示名：路径最后一段。 */
export function chipLabelOf(path: string): string {
  const segs = path.split(/[\\/]+/).filter(Boolean)
  return segs.length ? segs[segs.length - 1] : path
}

/**
 * 选择器状态机。组件侧用 reactive() 包装后即为响应式视图模型。
 */
export class PathPickerModel {
  /** 当前层列表结果（null = 无当前层：加载前/失败态）。 */
  listing: ListDirectoriesResult | null = null
  loading = false
  errorMsg = ''
  /** 已勾选路径（保持勾选顺序，chips 同序移除）。 */
  selected: string[] = []
  /** 展开状态：path → 展开（子层行内联在下方、缩进一层）。 */
  readonly expandedPaths = new Set<string>()
  private readonly cache = new Map<string, ListDirectoriesResult>()
  private readonly errors = new Map<string, string>()
  private readonly pending = new Set<string>()
  /** 初始加载的 home 兜底是否已用过（每次打开重置）。 */
  private fallbackTried = false

  constructor(private readonly load: PathLoader) {}

  // ---- 派生视图 ----

  /** 面包屑：当前 root 的全部祖先 + 当前层（末项）。 */
  get breadcrumb(): { name: string; path: string }[] {
    const root = this.listing?.root
    return root ? splitAncestors(root) : []
  }

  /** 展平当前层 + 各展开子层为行序列（depth 驱动缩进）。 */
  get rows(): PathPickerRow[] {
    const out: PathPickerRow[] = []
    const walk = (dirs: DirectoryEntry[], depth: number) => {
      for (const d of dirs) {
        const isOpen = this.expandedPaths.has(d.path)
        out.push({ kind: 'dir', name: d.name, path: d.path, depth, expanded: isOpen })
        if (isOpen) {
          const cached = this.cache.get(d.path)
          if (cached) {
            walk(cached.dirs, depth + 1)
          } else if (this.errors.has(d.path)) {
            out.push({
              kind: 'error',
              depth: depth + 1,
              path: d.path,
              message: this.errors.get(d.path) || '加载失败',
            })
          } else {
            out.push({ kind: 'loading', depth: depth + 1, key: `${d.path}#loading` })
          }
        }
      }
    }
    if (this.listing) walk(this.listing.dirs, 0)
    return out
  }

  /** 无勾选时确认不可用。 */
  get canConfirm(): boolean {
    return this.selected.length > 0
  }

  get confirmLabel(): string {
    return this.canConfirm ? `确认(${this.selected.length})` : '确认'
  }

  // ---- 勾选（有序集合） ----

  isSelected(path: string): boolean {
    return this.selected.includes(path)
  }

  toggleCheck(path: string): void {
    const idx = this.selected.indexOf(path)
    if (idx >= 0) {
      this.selected.splice(idx, 1)
    } else {
      this.selected.push(path)
    }
  }

  // ---- 展开下一层（懒加载 + 缓存 + 行内错误） ----

  async toggleExpand(path: string, forceReload = false): Promise<void> {
    if (this.expandedPaths.has(path) && !forceReload) {
      this.expandedPaths.delete(path)
      return
    }
    this.expandedPaths.add(path)
    await this.loadChildren(path)
  }

  private async loadChildren(path: string): Promise<void> {
    if (this.cache.has(path) || this.pending.has(path)) return
    this.pending.add(path)
    this.errors.delete(path)
    try {
      this.cache.set(path, await this.load(path))
    } catch (err) {
      this.errors.set(path, errText(err))
    } finally {
      this.pending.delete(path)
    }
  }

  // ---- 导航 ----

  /** 显式导航（面包屑/目录名/上一级）：不使用 home 兜底，失败走错误态+重试。 */
  navigate(path: string): void {
    if (this.loading) return
    this.fallbackTried = true
    void this.loadRoot(path, false)
  }

  /** 上一级（用返回的 parent；根目录 parent=null 不可用）。 */
  goParent(): void {
    const parent = this.listing?.parent
    if (parent) this.navigate(parent)
  }

  /** 错误态重试：重载当前 root（listing 已丢时按初始 root 的 home 兜底）。 */
  retry(): void {
    void this.loadRoot(this.listing?.root || '', false)
  }

  // ---- 打开 / 初始加载 ----

  /** 打开选择器：重置全部状态并加载初始 root（失败降级空串重试一次）。 */
  async resetAndLoad(workDir: string): Promise<void> {
    this.listing = null
    this.errorMsg = ''
    this.selected = []
    this.expandedPaths.clear()
    this.cache.clear()
    this.errors.clear()
    this.pending.clear()
    this.fallbackTried = false
    const root = initialRootFor(workDir)
    await this.loadRoot(root, root !== '')
  }

  private async loadRoot(root: string, allowFallback: boolean): Promise<void> {
    this.loading = true
    this.errorMsg = ''
    try {
      this.listing = await this.load(root)
    } catch (err) {
      if (allowFallback && !this.fallbackTried) {
        this.fallbackTried = true
        await this.loadRoot('', false)
        return
      }
      this.listing = null
      this.errorMsg = `读取目录失败：${errText(err)}`
    } finally {
      this.loading = false
    }
  }
}
