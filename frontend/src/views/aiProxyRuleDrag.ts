/**
 * AI 代理分流规则卡片的拖拽排序纯逻辑。
 *
 * 视图层用 Pointer Events 自绘拖拽（macOS WKWebView 不触发 HTML5 DnD 的 dragstart），
 * 这里沉淀可单测的阈值判定与数组搬移，DOM 事件接线留在 AIProxyView.vue。
 */

/** 拖拽启动阈值（像素）：指针位移达到该值才进入拖拽态，保留手柄的点击与聚焦。 */
export const DRAG_THRESHOLD_PX = 4

/** 拖拽会话起点：pointerdown 时记录，用于阈值判定与多指过滤。 */
export interface DragStartPoint {
  pointerId: number
  startX: number
  startY: number
}

/** 指针位移是否已达到拖拽阈值。 */
export function passesDragThreshold(start: DragStartPoint, x: number, y: number): boolean {
  const dx = x - start.startX
  const dy = y - start.startY
  return Math.hypot(dx, dy) >= DRAG_THRESHOLD_PX
}

/** 由命中卡片 id 推导放置目标：命中自身或未命中任何卡片时返回空串（不让位）。 */
export function dropTargetIdOf(hitCardId: string, draggingId: string): string {
  return hitCardId && hitCardId !== draggingId ? hitCardId : ''
}

/**
 * 活排序搬移：把 sourceId 项移动到 targetId 项当前所在下标（悬停让位语义）。
 * 原地修改数组；未命中任一 id 或指向自身时不修改并返回 false，发生搬移返回 true。
 */
export function moveItemById<T>(items: T[], sourceId: string, targetId: string, idOf: (item: T) => string): boolean {
  const from = items.findIndex(item => idOf(item) === sourceId)
  const to = items.findIndex(item => idOf(item) === targetId)
  if (from < 0 || to < 0 || from === to) return false
  const [moved] = items.splice(from, 1)
  items.splice(to, 0, moved)
  return true
}
