import { describe, expect, it } from 'vitest'
import {
  DRAG_THRESHOLD_PX,
  dropTargetIdOf,
  moveItemById,
  passesDragThreshold,
  type DragStartPoint,
} from '../../views/aiProxyRuleDrag'

const start: DragStartPoint = { pointerId: 1, startX: 100, startY: 100 }

describe('passesDragThreshold', () => {
  it('位移小于阈值时不进入拖拽', () => {
    expect(passesDragThreshold(start, 100 + DRAG_THRESHOLD_PX - 0.01, 100)).toBe(false)
    expect(passesDragThreshold(start, 100, 100)).toBe(false)
  })

  it('位移达到阈值（任意方向）即进入拖拽', () => {
    expect(passesDragThreshold(start, 100 + DRAG_THRESHOLD_PX, 100)).toBe(true)
    expect(passesDragThreshold(start, 100 - DRAG_THRESHOLD_PX, 100)).toBe(true)
    expect(passesDragThreshold(start, 100, 100 - DRAG_THRESHOLD_PX)).toBe(true)
    // 3-4-5 三角：斜向合成位移同样过阈
    expect(passesDragThreshold(start, 100 + 3, 100 - 3)).toBe(true)
  })
})

describe('dropTargetIdOf', () => {
  it('命中的卡片本身即放置目标', () => {
    expect(dropTargetIdOf('rule-b', 'rule-a')).toBe('rule-b')
  })

  it('命中自身或空白处返回空串（不让位）', () => {
    expect(dropTargetIdOf('rule-a', 'rule-a')).toBe('')
    expect(dropTargetIdOf('', 'rule-a')).toBe('')
  })
})

describe('moveItemById', () => {
  const idOf = (item: { id: string }) => item.id
  const list = (ids: string[]) => ids.map(id => ({ id }))

  it('向后搬移：拖到下方位次的卡片上，源项落在其下标', () => {
    const items = list(['a', 'b', 'c', 'd'])
    expect(moveItemById(items, 'a', 'c', idOf)).toBe(true)
    expect(items.map(idOf)).toEqual(['b', 'c', 'a', 'd'])
  })

  it('向前搬移：拖到上方位次的卡片上，源项落在其下标', () => {
    const items = list(['a', 'b', 'c', 'd'])
    expect(moveItemById(items, 'd', 'b', idOf)).toBe(true)
    expect(items.map(idOf)).toEqual(['a', 'd', 'b', 'c'])
  })

  it('相邻卡片同样完成让位交换', () => {
    const items = list(['a', 'b'])
    expect(moveItemById(items, 'a', 'b', idOf)).toBe(true)
    expect(items.map(idOf)).toEqual(['b', 'a'])
  })

  it('指向自身或未命中 id 时不修改数组', () => {
    const items = list(['a', 'b', 'c'])
    expect(moveItemById(items, 'b', 'b', idOf)).toBe(false)
    expect(moveItemById(items, 'b', 'missing', idOf)).toBe(false)
    expect(moveItemById(items, 'missing', 'b', idOf)).toBe(false)
    expect(items.map(idOf)).toEqual(['a', 'b', 'c'])
  })

  it('原地搬移保留项实例（同一对象引用）', () => {
    const items = list(['a', 'b', 'c'])
    const moved = items[0]
    moveItemById(items, 'a', 'c', idOf)
    expect(items[2]).toBe(moved)
  })
})
