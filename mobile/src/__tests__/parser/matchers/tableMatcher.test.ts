import { buildTableBlock, isTableBlock, tableMatcher } from '../../../parser/matchers/tableMatcher'

describe('tableMatcher', () => {
  it('detects pipe tables with header separator and rows', () => {
    expect(isTableBlock(['| A | B |', '| --- | --- |', '| 1 | 2 |'])).toBe(true)
  })

  it('supports alignment markers in separator rows', () => {
    expect(isTableBlock(['| A | B |', '| :--- | ---: |', '| 1 | 2 |'])).toBe(true)
  })

  it('rejects tables without a separator row', () => {
    expect(isTableBlock(['| A | B |', '| 1 | 2 |'])).toBe(false)
  })

  it('rejects single-line pipe content', () => {
    expect(isTableBlock(['| A | B |'])).toBe(false)
  })

  it('rejects plain text', () => {
    expect(isTableBlock(['plain text', 'next line'])).toBe(false)
  })

  it('builds table blocks with correct headers', () => {
    expect(buildTableBlock({
      appType: 'opencode',
      lines: ['| Name | Status |', '| --- | --- |', '| API | Ready |'],
      raw: '| Name | Status |\n| --- | --- |\n| API | Ready |',
      createdAt: 22,
    }).headers).toEqual(['Name', 'Status'])
  })

  it('builds table blocks with correct rows', () => {
    expect(buildTableBlock({
      appType: 'claudecode',
      lines: ['| Name | Status |', '| --- | --- |', '| API | Ready |', '| UI | WIP |'],
      raw: '| Name | Status |\n| --- | --- |\n| API | Ready |\n| UI | WIP |',
      createdAt: 23,
    }).rows).toEqual([
      ['API', 'Ready'],
      ['UI', 'WIP'],
    ])
  })

  it('supports header-only tables', () => {
    expect(buildTableBlock({
      appType: 'claudecode',
      lines: ['| A | B |', '| --- | --- |'],
      raw: '| A | B |\n| --- | --- |',
      createdAt: 24,
    })).toEqual({
      id: 'table-24',
      type: 'table',
      appType: 'claudecode',
      raw: '| A | B |\n| --- | --- |',
      headers: ['A', 'B'],
      rows: [],
      content: '| A | B |\n| --- | --- |',
      createdAt: 24,
    })
  })

  it('preserves raw content', () => {
    expect(buildTableBlock({
      appType: 'opencode',
      lines: ['| A | B |', '| --- | --- |', '| 1 | 2 |'],
      raw: '| A | B |\n| --- | --- |\n| 1 | 2 |',
      createdAt: 25,
    }).content).toBe('| A | B |\n| --- | --- |\n| 1 | 2 |')
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'claudecode' as const,
      lines: ['| A | B |', '| --- | --- |', '| 1 | 2 |'],
      raw: '| A | B |\n| --- | --- |\n| 1 | 2 |',
      createdAt: 10,
    }
    expect(tableMatcher.match(context)).toBe(true)
    expect(tableMatcher.build(context).type).toBe('table')
  })
})
