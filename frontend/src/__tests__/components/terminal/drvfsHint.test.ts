import { describe, expect, it } from 'vitest'
import { drvfsHintFor } from '../../../components/terminal/drvfsHint'
import type { DrvFSAdvisoryEntry } from '../../../api/drvfsAdvisory'

function entry(over: Partial<DrvFSAdvisoryEntry>): DrvFSAdvisoryEntry {
  return {
    source: 'pty',
    workDir: '',
    mountPath: '',
    detail: '',
    at: 1,
    ...over,
  }
}

describe('drvfsHintFor（P4-A1/C1：失败处关联提示匹配）', () => {
  it('路径等于记录的 workDir：命中并携带挂载点', () => {
    const hint = drvfsHintFor(
      [entry({ source: 'gitassist', workDir: '/mnt/x/repo', mountPath: '/mnt/x/repo', at: 10 })],
      '/mnt/x/repo',
    )
    expect(hint).toContain('该目录为 DrvFS 挂载（/mnt/x/repo）')
  })

  it('路径落在 mountPath 之下：命中（含挂载点尾斜杠归一）', () => {
    const hint = drvfsHintFor(
      [entry({ source: 'paths', workDir: '/mnt/e', mountPath: '/mnt/e/', at: 10 })],
      '/mnt/e/drvfs-probe',
    )
    expect(hint).toContain('（/mnt/e）')
  })

  it('多条命中时取最新（at 最大）的一条', () => {
    const hint = drvfsHintFor(
      [
        entry({ source: 'paths', workDir: '/mnt/e', mountPath: '/mnt/e', at: 10 }),
        entry({ source: 'pty', workDir: '/mnt/e/sub', mountPath: '/mnt/e/sub', at: 99 }),
      ],
      '/mnt/e/sub/deeper',
    )
    expect(hint).toContain('挂载（/mnt/e/sub）')
    expect(hint).not.toContain('挂载（/mnt/e）')
  })

  it('ext4 路径与非盘符挂载不命中', () => {
    const entries = [entry({ source: 'pty', workDir: 'D:\\repo', mountPath: '/mnt/d/repo' })]
    expect(drvfsHintFor(entries, '/home/u/repo')).toBeNull()
    expect(drvfsHintFor(entries, '/mnt/wsl/fonts')).toBeNull() // 多字母段：非盘符挂载
  })

  it('mountPath 为空的记录跳过；空路径/空列表返回 null', () => {
    expect(drvfsHintFor([entry({ workDir: '/home/u' })], '/home/u/x')).toBeNull()
    expect(drvfsHintFor([], '/mnt/x/repo')).toBeNull()
    expect(drvfsHintFor([entry({ mountPath: '/mnt/x' })], '')).toBeNull()
  })

  it('部分前缀但不构成路径段（/mnt/x2 vs /mnt/x）不误命中', () => {
    const entries = [entry({ source: 'pty', workDir: 'X:\\', mountPath: '/mnt/x' })]
    expect(drvfsHintFor(entries, '/mnt/x2/other')).toBeNull()
  })
})
