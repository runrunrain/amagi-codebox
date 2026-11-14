/**
 * 桌面 /terminal 视图「终端 ⇄ 网页」双平面的会话类型闸门（纯逻辑，便于单测覆盖）。
 *
 * 与 UI 解耦的纯函数：
 * - webPlaneCapable：该 AppType 的会话是否具备 Web 平面（切换控件 + webui 探测的前提）
 * - autoPreferWeb：webui available 时是否自动把活动会话切到 Web 平面
 * - webPlaneToggleTitle：切换控件的悬浮提示语
 *
 * 这里只回答「会话类型上是否可能有 Web 平面」；平面是否真的可用仍由 webui 探测
 * 状态机决定（webui.Service 五态，按 sessionId 键控、与 appType 无关）。不具备平面的
 * 具体会话（WSL 目标 / 外部终端 / 关闭开关的 claudecode 会话）不会注册 tracker，
 * 探测恒为 unknown，切换控件自然不出现——无需在此枚举降级形态。
 *
 * 数据面宿主的差别（pi = amagi-pi 扩展进程；claudecode = CodeBox 进程内
 * internal/claudewebui）对本闸门透明：两者共用同一探测契约与 WebPlaneHost 容器。
 */

/**
 * 具备 Web 平面的会话 AppType 集合（取值对齐 internal/session/types.go 的 AppType）。
 * omp/opencode/codex 无 Web 平面数据面，不在集合内。
 */
export const WEB_PLANE_APP_TYPES = ['pi', 'claudecode'] as const

/** 具备 Web 平面的会话 AppType。 */
export type WebPlaneAppType = (typeof WEB_PLANE_APP_TYPES)[number]

/**
 * 该 AppType 的会话是否具备 Web 平面。
 *
 * 严格等值匹配（不 trim、不忽略大小写）：appType 来自后端 SessionInfo 的规范化
 * 枚举值，宽松匹配只会把脏数据放进闸门。undefined（会话尚未解析）/ 空串 / 未知类型
 * 一律 false。
 */
export function webPlaneCapable(appType?: string): appType is WebPlaneAppType {
  return (WEB_PLANE_APP_TYPES as readonly string[]).includes(appType ?? '')
}

/**
 * webui 探测 available 时，是否自动把活动会话切到 Web 平面。
 *
 * 仅 'pi'（design.md §4-D5）：
 * - pi 的 Web 平面只有「装了 web 插件」才 available——available 本身就是用户意图的
 *   信号，自动切 Web 是该前提下的既有需求。
 * - claudecode 的 Web 平面是 CodeBox 内置的，embedded 会话几乎恒 available；而
 *   claude 的 TUI 是其既有默认平面。若照 pi 自动切，等于每个 claude 会话的默认视图
 *   都被内置 webui 劫持。因此 claude 只提供切换控件，由用户显式进入 Web 平面。
 *
 * 该函数不蕴含 webPlaneCapable 之外的任何可用性判断，调用方仍需自行叠加
 * 「探测 available / 会话 running / 用户未 pin TUI」等守卫。
 */
export function autoPreferWeb(appType?: string): boolean {
  return appType === 'pi'
}

/**
 * 「终端 ⇄ 网页」切换控件的悬浮提示语。
 *
 * 具备平面的类型给出带 CLI 名的提示；其余（含 undefined）返回空串——这些会话
 * 本就不显示切换控件，空串使 title 属性不渲染。
 */
export function webPlaneToggleTitle(appType?: string): string {
  if (!webPlaneCapable(appType)) return ''
  switch (appType) {
    case 'pi':
      return '在终端与 Pi 网页平面之间切换'
    case 'claudecode':
      return '在终端与 Claude Code 网页平面之间切换'
  }
}
