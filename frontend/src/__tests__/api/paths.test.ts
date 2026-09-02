import { describe, expect, it, vi } from 'vitest'

// 在 wailsjs 绑定边界打桩：api 包装层的职责是透传参数 + JSON.parse 载荷。
const { listDirectoriesBinding } = vi.hoisted(() => ({
  listDirectoriesBinding: vi.fn(),
}))

vi.mock('../../../wailsjs/go/paths/PathsService', () => ({
  GetPaths: vi.fn(),
  AddPath: vi.fn(),
  RemovePath: vi.fn(),
  GetDefaultPath: vi.fn(),
  SetDefaultPath: vi.fn(),
  UpdateLabel: vi.fn(),
  ValidatePath: vi.fn(),
  Load: vi.fn(),
  Save: vi.fn(),
  ListDirectories: listDirectoriesBinding,
}))

import { listDirectories } from '../../api/paths'

describe('api.paths.listDirectories', () => {
  it('透传 root 参数并解析后端 JSON 载荷为结构化结果', async () => {
    listDirectoriesBinding.mockResolvedValueOnce(
      JSON.stringify({
        root: '/Users/maorun',
        parent: '/Users',
        dirs: [
          { name: 'proj-a', path: '/Users/maorun/proj-a' },
          { name: 'proj-b', path: '/Users/maorun/proj-b' },
        ],
        truncated: false,
      }),
    )

    const result = await listDirectories('/Users/maorun')

    expect(listDirectoriesBinding).toHaveBeenCalledTimes(1)
    expect(listDirectoriesBinding).toHaveBeenCalledWith('/Users/maorun')
    expect(result).toEqual({
      root: '/Users/maorun',
      parent: '/Users',
      dirs: [
        { name: 'proj-a', path: '/Users/maorun/proj-a' },
        { name: 'proj-b', path: '/Users/maorun/proj-b' },
      ],
      truncated: false,
    })
  })

  it('parent 为文件系统根时保持 null 直通', async () => {
    listDirectoriesBinding.mockResolvedValueOnce(
      JSON.stringify({ root: '/', parent: null, dirs: [], truncated: false }),
    )

    const result = await listDirectories('/')

    expect(result.parent).toBeNull()
    expect(result.dirs).toEqual([])
    expect(result.truncated).toBe(false)
  })

  it('truncated 标志位透传', async () => {
    listDirectoriesBinding.mockResolvedValueOnce(
      JSON.stringify({ root: '/big', parent: '/', dirs: [], truncated: true }),
    )

    await expect(listDirectories('/big')).resolves.toMatchObject({ truncated: true })
  })

  it('绑定报错时原样 rethrow（callApi 契约）', async () => {
    listDirectoriesBinding.mockRejectedValueOnce(new Error('boom'))
    await expect(listDirectories('/x')).rejects.toThrow('boom')
  })
})
