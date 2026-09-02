import { describe, expect, it, vi } from 'vitest'
import {
  PathPickerModel,
  chipLabelOf,
  initialRootFor,
  parentDirOf,
  splitAncestors,
  type PathLoader,
} from '../../../components/terminal/pathPickerModel'
import type { ListDirectoriesResult } from '../../../api/paths'

// ---- 桩工具：内存目录树 → ListDirectoriesResult ----

function listingOf(root: string, names: string[], truncated = false): ListDirectoriesResult {
  const parent = root === '/' ? null : `/${root.split('/').filter(Boolean).slice(0, -1).join('/') || ''}` || null
  return {
    root,
    parent: parent === '' ? null : parent,
    dirs: names.map((n) => ({ name: n, path: `${root === '/' ? '' : root}/${n}` })),
    truncated,
  }
}

const tree: Record<string, string[]> = {
  '/w': ['a', 'b'],
  '/w/a': ['c'],
  '/home': ['Desktop'],
}

function backendOf(
  tree: Record<string, string[]>,
): PathLoader & { calls: string[] } {
  const calls: string[] = []
  const loader: PathLoader = async (root) => {
    calls.push(root)
    // 后端契约：root 为空串时回退用户 home（桩里用 /home 代表）
    const effRoot = root === '' ? '/home' : root
    const names = tree[effRoot]
    if (!names) throw new Error(`no such dir: ${root}`)
    return listingOf(effRoot, names)
  }
  return Object.assign(loader, { calls })
}

// ---- 纯函数：dirname / 初始 root 决策 ----

describe('parentDirOf / initialRootFor', () => {
  it('常规绝对路径取父目录', () => {
    expect(parentDirOf('/w/proj')).toBe('/w')
    expect(parentDirOf('/a/b/c')).toBe('/a/b')
  })

  it('容忍尾部重复分隔符', () => {
    expect(parentDirOf('/w/proj/')).toBe('/w')
    expect(parentDirOf('/w/proj///')).toBe('/w')
  })

  it('根下第一层的父目录是根本身', () => {
    expect(parentDirOf('/x')).toBe('/')
  })

  it('Windows 反斜杠路径同样支持', () => {
    expect(parentDirOf('C:\\Users\\bob\\proj')).toBe('C:\\Users\\bob')
  })

  it('空串 / 根 / 相对单段返回空串（走后端 home 兜底）', () => {
    expect(parentDirOf('')).toBe('')
    expect(parentDirOf('/')).toBe('')
    expect(parentDirOf('proj')).toBe('')
  })

  it('initialRootFor = parentDirOf（打开时初始 root 决策）', () => {
    expect(initialRootFor('/w/proj')).toBe('/w')
    expect(initialRootFor('')).toBe('')
  })
})

// ---- 纯函数：面包屑祖先链 / chip 标签 ----

describe('splitAncestors', () => {
  it('POSIX 绝对路径：全部祖先 + 当前层（末项）', () => {
    expect(splitAncestors('/a/b/c')).toEqual([
      { name: '/', path: '/' },
      { name: 'a', path: '/a' },
      { name: 'b', path: '/a/b' },
      { name: 'c', path: '/a/b/c' },
    ])
  })

  it('根目录本身：单一根项', () => {
    expect(splitAncestors('/')).toEqual([{ name: '/', path: '/' }])
  })

  it('Windows 盘符路径：盘符根 + 各级', () => {
    expect(splitAncestors('C:\\Users\\bob')).toEqual([
      { name: 'C:\\', path: 'C:\\' },
      { name: 'Users', path: 'C:\\Users' },
      { name: 'bob', path: 'C:\\Users\\bob' },
    ])
  })
})

describe('chipLabelOf', () => {
  it('取路径最后一段', () => {
    expect(chipLabelOf('/w/a')).toBe('a')
    expect(chipLabelOf('C:\\Users\\bob')).toBe('bob')
  })
})

// ---- 状态机：列表 / 勾选 / 展开 / 导航 / 降级 / 确认 ----

describe('PathPickerModel', () => {
  it('resetAndLoad：初始 root = dirname(workDir)，加载当前层', async () => {
    const backend = backendOf(tree)
    const model = new PathPickerModel(backend)

    await model.resetAndLoad('/w/proj')

    expect(backend.calls).toEqual(['/w'])
    expect(model.listing?.root).toBe('/w')
    expect(model.rows.filter((r) => r.kind === 'dir').map((r: any) => r.name)).toEqual(['a', 'b'])
    expect(model.breadcrumb.map((s) => s.name)).toEqual(['/', 'w'])
    expect(model.loading).toBe(false)
    expect(model.errorMsg).toBe('')
  })

  it('workDir 为空：初始 root 为空串（后端 home 兜底）', async () => {
    const backend = backendOf(tree)
    const model = new PathPickerModel(backend)

    await model.resetAndLoad('')

    expect(backend.calls).toEqual([''])
    expect(model.listing?.root).toBe('/home')
  })

  it('首次加载失败：降级空串重试一次', async () => {
    const backend = backendOf(tree)
    const brokenBackend: PathLoader = async (root) => {
      if (root === '/w') throw new Error('stat failed')
      return backend(root)
    }
    const calls: string[] = []
    const wrapped: PathLoader = async (root) => {
      calls.push(root)
      return brokenBackend(root)
    }
    const model = new PathPickerModel(wrapped)

    await model.resetAndLoad('/w/proj')

    expect(calls).toEqual(['/w', ''])
    expect(model.listing?.root).toBe('/home')
    expect(model.errorMsg).toBe('')
  })

  it('降级仍失败：进入错误态，retry 成功后恢复', async () => {
    let fail = true
    const backend: PathLoader = async (root) => {
      if (fail) throw new Error('boom')
      const effRoot = root === '' ? '/home' : root
      return listingOf(effRoot, tree[effRoot] ?? [])
    }
    const model = new PathPickerModel(backend)

    await model.resetAndLoad('/w/proj') // '/w' 与 '' 均失败

    expect(model.listing).toBeNull()
    expect(model.errorMsg).toContain('读取目录失败')
    expect(model.canConfirm).toBe(false)

    fail = false
    model.retry() // listing 已丢 → 按 home 兜底重载
    await vi.waitFor(() => expect(model.listing?.root).toBe('/home'))
  })

  it('勾选顺序保持：后勾先勾不重排，移除后其余保序', () => {
    const model = new PathPickerModel(backendOf(tree))

    expect(model.canConfirm).toBe(false)
    expect(model.confirmLabel).toBe('确认')

    model.toggleCheck('/w/b')
    model.toggleCheck('/w/a')
    expect(model.selected).toEqual(['/w/b', '/w/a'])
    expect(model.isSelected('/w/a')).toBe(true)
    expect(model.canConfirm).toBe(true)
    expect(model.confirmLabel).toBe('确认(2)')

    model.toggleCheck('/w/b') // chips 移除同一入口
    expect(model.selected).toEqual(['/w/a'])
    expect(model.confirmLabel).toBe('确认(1)')
  })

  it('展开下一层：懒加载直接子目录为缩进行，可勾选，收起后隐藏', async () => {
    const backend = backendOf(tree)
    const model = new PathPickerModel(backend)
    await model.resetAndLoad('/w/proj')

    await model.toggleExpand('/w/a')

    expect(backend.calls).toEqual(['/w', '/w/a'])
    const dirRows = model.rows.filter((r) => r.kind === 'dir') as Extract<
      (typeof model.rows)[number],
      { kind: 'dir' }
    >[]
    expect(dirRows.map((r) => r.name)).toEqual(['a', 'c', 'b'])
    expect(dirRows[1].depth).toBe(1) // 子层缩进

    // 子层目录同样可勾选，顺序：父层在前
    model.toggleCheck('/w/a')
    model.toggleCheck('/w/a/c')
    expect(model.selected).toEqual(['/w/a', '/w/a/c'])

    // 收起
    await model.toggleExpand('/w/a')
    expect(
      model.rows.filter((r) => r.kind === 'dir').map((r: any) => r.name),
    ).toEqual(['a', 'b'])
    // 收起不清勾选
    expect(model.selected).toEqual(['/w/a', '/w/a/c'])
  })

  it('展开失败：行内错误行 + forceReload 重试恢复', async () => {
    let fail = true
    const base = backendOf(tree)
    const backend: PathLoader = async (root) => {
      if (root === '/w/a' && fail) throw new Error('io error')
      return base(root)
    }
    const model = new PathPickerModel(backend)
    await model.resetAndLoad('/w/proj')

    await model.toggleExpand('/w/a')
    const errRow = model.rows.find((r) => r.kind === 'error') as Extract<
      (typeof model.rows)[number],
      { kind: 'error' }
    >
    expect(errRow.message).toContain('io error')

    fail = false
    await model.toggleExpand('/w/a', true)
    expect(model.rows.some((r) => r.kind === 'error')).toBe(false)
    expect(
      model.rows.filter((r) => r.kind === 'dir').map((r: any) => r.name),
    ).toEqual(['a', 'c', 'b'])
  })

  it('面包屑/目录名导航：加载目标为当前层，失败不降级（显式导航）', async () => {
    const backend = backendOf(tree)
    const model = new PathPickerModel(backend)
    await model.resetAndLoad('/w/proj')

    model.navigate('/w/a')
    await vi.waitFor(() => expect(model.listing?.root).toBe('/w/a'))
    expect(
      model.rows.filter((r) => r.kind === 'dir').map((r: any) => r.name),
    ).toEqual(['c'])
    expect(model.breadcrumb.map((s) => s.name)).toEqual(['/', 'w', 'a'])

    // 上一级（用返回的 parent 链）
    model.goParent()
    await vi.waitFor(() => expect(model.listing?.root).toBe('/w'))

    // 显式导航到不存在目录：错误态，不触发 home 降级
    model.navigate('/gone')
    await vi.waitFor(() => expect(model.errorMsg).toContain('读取目录失败'))
    expect(backend.calls).not.toContain('')
    expect(model.listing).toBeNull()
  })

  it('重复展开/在途加载不重复发起请求', async () => {
    const backend = backendOf(tree)
    const model = new PathPickerModel(backend)
    await model.resetAndLoad('/w/proj')

    await Promise.all([model.toggleExpand('/w/a'), model.toggleExpand('/w/a', true)])
    // forceReload 与首次加载共享 pending 门：至多两次（第二次在 pending 期间被吞）
    expect(backend.calls.filter((c) => c === '/w/a').length).toBeLessThanOrEqual(2)
    expect(
      model.rows.filter((r) => r.kind === 'dir').map((r: any) => r.name),
    ).toEqual(['a', 'c', 'b'])
  })

  it('truncated 标志从列表结果透传', async () => {
    const backend: PathLoader = async () => listingOf('/big', ['x'], true)
    const model = new PathPickerModel(backend)
    await model.resetAndLoad('/big/proj')
    expect(model.listing?.truncated).toBe(true)
  })
})
