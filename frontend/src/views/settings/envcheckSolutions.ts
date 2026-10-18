/**
 * 环境检测 issue→solution 执行决策（纯逻辑，无 Vue/Wails 依赖）。
 *
 * 从 EnvCheckSettings.vue 抽出的可单测决策层：后端 solution type → 前端执行
 * 策略的映射、确认文案、FixActionResult→横幅组装、修复后刷新策略、异步操作
 * 终态横幅。组件层只做绑定调用与状态落地，决策全部走本模块，镜像测试位于
 * src/__tests__/views/settings/envcheckSolutions.test.ts。
 *
 * 关键背景（20261017 E2 修复的根因，勿回退）：
 * - restart_app 不在后端 RunFixAction 白名单（fix_dispatcher.go allowedFixActions），
 *   前端必须本地提示重启，绝不能透传后端（否则永远报 unsupported fix action）。
 * - install_claude_method 走 App.RunEnvFixAction(action, tool, extraPath) 无法
 *   携带 method（三参签名无 method），必须走 StartInstallClaudeWithMethodAsync。
 * - uninstall_tool（E1 共享契约 shared-contract.md）：FixActionResult.success 只
 *   表示「已受理」，真实终态以 snapshot.operation(kind=uninstall) 为准。
 */

/** 与 wailsjs envcheck.ResolutionAction 结构兼容的最小类型（纯模块不引 wailsjs）。 */
export interface SolutionLike {
  type: string
  description?: string
  command?: string
  tool?: string
  packageName?: string
  requiresConfirm?: boolean
  isPrimary?: boolean
  method?: string
}

export type SolutionPlan =
  | { kind: 'display_only' }
  | { kind: 'restart_notice' }
  | { kind: 'full_check' }
  | { kind: 'async_install' }
  | { kind: 'claude_method_install'; method: string }
  | { kind: 'clean_claude'; method: string }
  | { kind: 'uninstall_tool' }
  | { kind: 'fix_action' }

export interface PlanContext {
  /** issue 所属卡片 key（solution.tool 缺失时的兜底）。 */
  cardKey: string
  /** Claude 安装方式选择器当前值（''/native/npm）。 */
  claudePickerMethod?: string
  /** snapshot 中 claude_code 的 installMethod（可能为空或过期）。 */
  claudeStatusInstallMethod?: string
}

/**
 * 四个 Agent CLI：卸载走 E1 契约 uninstall_tool。
 * Claude 沿用按来源卸载（UninstallClaudeCode/CleanClaudeInstall），
 * Headroom 沿用租约保护的 CleanHeadroom，均不经本列表。
 */
const AGENT_CLI_TOOLS: readonly string[] = ['opencode', 'codex', 'pi', 'omp']

export function isAgentCLITool(key: string): boolean {
  return AGENT_CLI_TOOLS.includes(key)
}

function claudeCleanMethod(sol: SolutionLike, ctx: PlanContext): string {
  // F-1 顺序：solution 显式 method > snapshot.installMethod > 'native'。
  // 选择器值不参与 clean 决策（用户选择的安装渠道 ≠ 当前安装的渠道）。
  return sol.method || ctx.claudeStatusInstallMethod || 'native'
}

function claudeInstallMethod(sol: SolutionLike, ctx: PlanContext): string {
  // 安装方向相反：用户显式选择（picker）优先于可能过期的 snapshot 状态。
  // solution 显式 method（后端钉死渠道时）最优先。
  return sol.method || ctx.claudePickerMethod || ctx.claudeStatusInstallMethod || 'native'
}

/**
 * 把后端 solution type 映射为前端执行策略。
 *
 * 后端会实际发出的 type（checker_*）全集：fix_path / install_node /
 * install_tool / retry / manual_command / restart_app / install_wsl_search_tools /
 * install_claude_method / clean_claude_install；E1 契约新增 uninstall_tool。
 * 其余 type（含未来后端新增且已在 RunFixAction 白名单内的）透传 fix_action。
 */
export function planSolutionExecution(sol: SolutionLike, ctx: PlanContext): SolutionPlan {
  switch (sol.type) {
    case 'manual_command':
      return { kind: 'display_only' }
    case 'restart_app':
      // 后端白名单不含 restart_app（types.go 定义了但 fix_dispatcher 不受理），
      // 透传必然报 unsupported fix action —— 本地提示重启即可。
      return { kind: 'restart_notice' }
    case 'retry':
      return { kind: 'full_check' }
    case 'install_tool':
      // 异步安装入口（StartInstallToolAsync）带进度/终态横幅/自动刷新，
      // 优于 RunEnvFixAction 的 install_tool 分支（受理后无刷新无进度）。
      return { kind: 'async_install' }
    case 'install_claude_method':
      // RunEnvFixAction 三参签名带不了 method，必须走专用异步绑定。
      return { kind: 'claude_method_install', method: claudeInstallMethod(sol, ctx) }
    case 'clean_claude_install':
      return { kind: 'clean_claude', method: claudeCleanMethod(sol, ctx) }
    case 'uninstall_tool':
      return { kind: 'uninstall_tool' }
    default:
      // fix_path / install_node / install_wsl_search_tools 等白名单动作。
      return { kind: 'fix_action' }
  }
}

export function solutionLabel(type: string): string {
  switch (type) {
    case 'fix_path': return '修复 PATH'
    case 'install_tool': return '安装工具'
    case 'install_node': return '安装 Node.js'
    case 'retry': return '重新检测'
    case 'clean_claude_install': return '清理 Claude 安装'
    case 'manual_command': return '查看手动命令'
    case 'restart_app': return '重启应用'
    case 'install_claude_method': return '重装 Claude Code'
    case 'fix_claude_config': return '修复 Claude 配置'
    case 'install_wsl_search_tools': return '安装搜索工具'
    case 'uninstall_tool': return '卸载'
    default: return '解决方案'
  }
}

export function operationKindLabel(kind: string): string {
  if (kind === 'install') return '安装'
  if (kind === 'update') return '更新'
  if (kind === 'uninstall') return '卸载'
  return kind || '操作'
}

/**
 * 需要确认的 solution 返回确认文案，否则返回 null（不需确认）。
 * 显式清单保持既有确认面；后端 requiresConfirm=true 的其余动作（如
 * install_claude_method、npm shim 修复重装）走通用兜底文案。
 */
export function solutionConfirmMessage(sol: SolutionLike, displayName: string): string | null {
  switch (sol.type) {
    case 'fix_path':
      return '此操作将备份并修改 PATH 配置，以加入必要的工具目录。是否继续？'
    case 'install_node':
      return '此操作将尝试安装 Node.js。是否继续？'
    case 'install_wsl_search_tools':
      return '此操作将在 WSL 内以 root 安装 fd-find 与 ripgrep。是否继续？'
    case 'clean_claude_install':
      return `此操作将清理当前 ${displayName} 安装产物（保留 versions/ 二进制可复用）。是否继续？`
    case 'install_claude_method':
      return '此操作将重新安装 Claude Code。是否继续？'
    case 'uninstall_tool':
      return `确定要卸载 ${displayName} 吗？将移除 CodeBox 可识别的该工具安装产物（不删除用户配置），卸载后不会自动重新安装。`
    default:
      if (sol.requiresConfirm) {
        return `此操作将对 ${displayName} 执行「${solutionLabel(sol.type)}」。是否继续？`
      }
      return null
  }
}

/** 与 envcheck.CheckStatus 结构兼容的最小类型（仅顶层 solutions 去重所需字段）。 */
export interface StatusLike<T extends SolutionLike = SolutionLike> {
  issues?: { solutions?: T[] }[]
  solutions?: T[]
}

function solutionDedupeKey(sol: SolutionLike): string {
  return `${sol.type}|${sol.tool || ''}|${sol.method || ''}|${sol.command || ''}`
}

/**
 * 稳定 action 身份（无位置下标）：同一 solution 无论出现在 issue 下标 0/2
 * 还是顶层 solutions，key 一致 —— 供模板 loading key 与去重共用，修复集成
 * 批次「顶层修复按钮处理中不禁用」的 Major 2（旧实现带 idx，模板与
 * executeSolution 的 idx 永远对不上）。
 */
export function solutionIdentity(sol: SolutionLike): string {
  return solutionDedupeKey(sol)
}

export function solutionActionKey(sol: SolutionLike, cardKey: string): string {
  return `${cardKey}|${solutionIdentity(sol)}`
}

/**
 * 是否写操作类 solution：manual_command（纯展示）/ restart_app（本地提示）
 * 不算写操作，不受全局 busy 屏蔽；其余全部按写操作处理。
 */
export function isWriteSolution(type: string): boolean {
  return type !== 'manual_command' && type !== 'restart_app'
}

/**
 * 需要在卡片上单独渲染的顶层 solutions：后端会把同一动作同时挂到
 * issue.solutions 与 status.Solutions（service.go populateCanInstall /
 * checker_common.go 都有「also add to top-level」分支），不去重会出现
 * 双份「修复 PATH / 安装 Node.js」按钮；而 issue.solutions 为空、仅顶层
 * 有动作的场景（Leader dependency 夹具已证实）不渲染顶层就会完全没有
 * 依赖按钮。去重键 = type|tool|method|command（集成批次 Minor：身份必须含
 * tool）；cardTool 作为无 tool 副本的回退（后端两处副本要么都带 tool 要么
 * 都不带，回退保证混合形态仍能去重，且不同卡片互不合并）。
 */
export function visibleTopLevelSolutions<T extends SolutionLike>(
  status: StatusLike<T> | null | undefined,
  cardTool?: string,
): T[] {
  const top = status?.solutions || []
  if (!top.length) return []
  const keyOf = (s: SolutionLike): string =>
    `${s.type}|${s.tool || cardTool || ''}|${s.method || ''}|${s.command || ''}`
  const seen = new Set<string>()
  for (const issue of status?.issues || []) {
    for (const s of issue.solutions || []) {
      seen.add(keyOf(s))
    }
  }
  const result: T[] = []
  for (const s of top) {
    const key = keyOf(s)
    if (seen.has(key)) continue
    seen.add(key)
    result.push(s)
  }
  return result
}

/** 与 CheckStatus 安装能力字段结构兼容的最小类型。 */
export interface InstallCapabilityLike {
  canInstall?: boolean
  installBlockedReason?: string
}

/**
 * 通用安装/更新按钮的阻断原因：canInstall===false 时返回展示文案，
否则空串。
 * 不把 canInstall 缺失（旧 mock/异常快照）当阻断，避免误锁按钮。
 */
export function installBlockedReason(
  status: InstallCapabilityLike | null | undefined,
): string {
  if (!status || status.canInstall !== false) return ''
  return status.installBlockedReason || '当前环境不满足安装条件（依赖缺失）'
}

/** 与 envcheck.FixActionResult / InstallResult 结构兼容的最小类型。 */
export interface FixResultLike {
  success?: boolean
  message?: string
  error?: string
  backupPath?: string
  profilePath?: string
  addedPaths?: string[]
  nextSteps?: string[]
  changed?: boolean
  requiresRestart?: boolean
}

export interface ResultBanner {
  title: string
  description?: string
  type: 'success' | 'error'
}

/**
 * FixActionResult → 结果横幅。相对旧版的关键差异：
 * - requiresRestart=true 时追加「需要重启」提示行 —— fix_path 在当前 CodeBox
 *   进程内不会改变已继承的 PATH，不提示会被用户读成「修复无效」。
 */
export function buildFixResultBanner(result: FixResultLike | null | undefined): ResultBanner {
  const r = result || {}
  const ok = !!r.success
  const title = ok
    ? r.message || '修复操作已完成'
    : r.error || r.message || '修复操作失败'
  const parts: string[] = []
  if (r.backupPath) parts.push(`备份文件：${r.backupPath}`)
  if (r.profilePath) parts.push(`配置文件：${r.profilePath}`)
  if (Array.isArray(r.addedPaths) && r.addedPaths.length) {
    parts.push('已加入路径：' + r.addedPaths.join(', '))
  }
  if (Array.isArray(r.nextSteps) && r.nextSteps.length) {
    parts.push(r.nextSteps.join('. '))
  }
  if (r.requiresRestart) {
    // 措辞按 E1 契约校准：后端会同步刷新当前进程 PATH；仅当界面/终端仍看到
    // 旧环境时才需要重启，不做「运行中无法刷新」的绝对断言（集成批次 Minor）。
    parts.push('若界面仍显示旧 PATH：重启 CodeBox 后生效；外部终端请重启终端或 source 配置文件')
  }
  return { title, description: parts.join('\n') || undefined, type: ok ? 'success' : 'error' }
}

export interface PostFixRefreshPlan {
  /**
   * none：修复失败，不自动复检（错误横幅即重试入口，用户可再点同一按钮）。
   * snapshot：成功但未变更（后端分支内已同步重跑 CheckAll），拉一次快照即可。
   * full：成功且变更（App 层还会 go CheckAll() 异步重检，与本端拉取存在竞态），
   *       由本端同步 runEnvCheck() 做确定性复检 —— 不再依赖「刚好碰到
   *       status.checking=true」的观察窗口（集成批次 Major 3）。
   */
  recheck: 'none' | 'snapshot' | 'full'
}

/**
 * 修复动作后的复检策略（集成批次 Major 3 重写：pollUntilSettled 从未被消费，
 * 且「碰运气式轮询」会停在旧缓存）。
 */
export function planPostFixRefresh(result: FixResultLike | null | undefined): PostFixRefreshPlan {
  if (!result?.success) return { recheck: 'none' }
  if (result.changed) return { recheck: 'full' }
  return { recheck: 'snapshot' }
}

/** 与 envcheck.OperationState 结构兼容的最小类型。 */
export interface OperationLike {
  id?: string
  tool?: string
  kind?: string
  status?: string
  message?: string
  error?: string
  result?: { version?: string; message?: string; error?: string } | null
}

/**
 * 异步操作终态 → 结果横幅。非终态（running/idle）返回 null。
 * kind=uninstall 的受理（RunFixAction success）不是终态，不得走本函数报成功。
 */
export function operationCompletionBanner(
  op: OperationLike | null | undefined,
  toolDisplayName: (tool: string) => string,
): ResultBanner | null {
  if (!op) return null
  if (op.status !== 'succeeded' && op.status !== 'failed' && op.status !== 'timeout') {
    return null
  }
  const name = toolDisplayName(op.tool || '')
  const kind = operationKindLabel(op.kind || '')
  if (op.status === 'succeeded') {
    const ver = op.result?.version ? ` (v${op.result.version})` : ''
    const msg = op.result?.message || op.message || ''
    return { title: `${name} ${kind}成功${ver}`, description: msg || undefined, type: 'success' }
  }
  const errMsg = op.error || op.result?.error || ''
  const msg = op.message || ''
  const parts = [errMsg, msg].filter(Boolean)
  return {
    title: `${name} ${kind}失败`,
    description: parts.join(' - ') || undefined,
    type: 'error',
  }
}
