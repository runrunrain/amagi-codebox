import { describe, expect, it } from 'vitest'
import {
  ASSOCIATED_PATH_PREFIX,
  buildAssociatedPathLines,
  wrapBracketedPaste,
} from '../../utils/quickFunctions'

describe('buildAssociatedPathLines', () => {
  it('单条路径输出一行「关联工作路径：<绝对路径>」，尾部不带换行', () => {
    expect(buildAssociatedPathLines(['/Users/maorun/proj'])).toBe(
      '关联工作路径：/Users/maorun/proj',
    )
  })

  it('多条路径按传入（勾选）顺序逐行排列，\\n 连接', () => {
    expect(
      buildAssociatedPathLines(['/b', '/a', '/c']),
    ).toBe(['关联工作路径：/b', '关联工作路径：/a', '关联工作路径：/c'].join('\n'))
  })

  it('多行文本不包含尾部换行', () => {
    const out = buildAssociatedPathLines(['/x', '/y'])
    expect(out.endsWith('\n')).toBe(false)
    expect(out.split('\n')).toHaveLength(2)
  })

  it('空列表返回空串', () => {
    expect(buildAssociatedPathLines([])).toBe('')
  })

  it('每行都带统一前缀', () => {
    const lines = buildAssociatedPathLines(['/p1', '/p2', '/p3']).split('\n')
    for (const line of lines) {
      expect(line.startsWith(ASSOCIATED_PATH_PREFIX)).toBe(true)
    }
  })
})

describe('wrapBracketedPaste', () => {
  it('以 \\x1b[200~ 开头、\\x1b[201~ 结尾包裹原文', () => {
    expect(wrapBracketedPaste('hello')).toBe('\x1b[200~hello\x1b[201~')
  })

  it('多行文本整体包裹，不改动内容', () => {
    const text = '关联工作路径：/a\n关联工作路径：/b'
    expect(wrapBracketedPaste(text)).toBe(`\x1b[200~${text}\x1b[201~`)
  })

  it('空文本也产生完整包裹序列（保持包裹语义）', () => {
    expect(wrapBracketedPaste('')).toBe('\x1b[200~\x1b[201~')
  })
})
