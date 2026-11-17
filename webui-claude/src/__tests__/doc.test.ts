/**
 * doc.test.ts — StreamDoc 幂等 / 工具配对四态 / 缺口重放合并（design §6.3、
 * §7.2 doc、§11 SPA 行）。wire 记录形状对齐 internal/claudewebui/tailer.go
 * wireRecord。
 */
import { describe, expect, it } from 'vitest'
import type { TranscriptRecord } from '../data/source'
import { createStreamDoc, type ToolStatus } from '../state/doc'

function userMsg(uuid: string, text: string): TranscriptRecord {
  return { kind: 'user', uuid, isHuman: true, text }
}

function assistantMsg(uuid: string, extra: Partial<TranscriptRecord> = {}): TranscriptRecord {
  return { kind: 'assistant', uuid, ...extra }
}

function toolResultRow(uuid: string, results: { id: string; isError?: boolean; content?: string }[]): TranscriptRecord {
  return { kind: 'user', uuid, toolResults: results.map((r) => ({ ...r })) }
}

function statuses(doc: ReturnType<typeof createStreamDoc>): Map<string, ToolStatus> {
  const out = new Map<string, ToolStatus>()
  for (const msg of doc.messages) {
    for (const card of msg.tools) out.set(card.id, card.status)
  }
  return out
}

describe('uuid 幂等 upsert', () => {
  it('同一记录重复到达不产生重复消息，字段以最新为准', () => {
    const doc = createStreamDoc()
    const rec = userMsg('u1', '第一版')
    doc.applyRecords([rec])
    doc.applyRecords([{ ...rec, text: '第二版' }])
    expect(doc.messages).toHaveLength(1)
    expect(doc.messages[0].text).toBe('第二版')
  })

  it('乱序批次后仍按首次出现顺序排列', () => {
    const doc = createStreamDoc()
    doc.applyRecords([userMsg('u1', 'a'), userMsg('u2', 'b')])
    doc.applyRecords([userMsg('u3', 'c')])
    expect(doc.messages.map((m) => m.uuid)).toEqual(['u1', 'u2', 'u3'])
  })
})

describe('toolUses↔toolResults 按 id 配对四态', () => {
  it('无配对=running / isError=failed / 成功=success', () => {
    const doc = createStreamDoc()
    doc.applyRecords([
      userMsg('u1', '跑一下'),
      assistantMsg('a1', {
        toolUses: [
          { id: 't1', name: 'Bash', input: { command: 'ls' } },
          { id: 't2', name: 'Read', input: { file_path: 'x' } },
          { id: 't3', name: 'Grep' },
        ],
      }),
    ])
    let st = statuses(doc)
    expect(st.get('t1')).toBe('running')
    expect(st.get('t2')).toBe('running')
    expect(st.get('t3')).toBe('running')

    doc.applyRecords([
      toolResultRow('u2', [
        { id: 't1', content: 'ok' },
        { id: 't2', isError: true, content: 'boom' },
      ]),
    ])
    st = statuses(doc)
    expect(st.get('t1')).toBe('success')
    expect(st.get('t2')).toBe('failed')
    expect(st.get('t3')).toBe('running')

    // 工具卡挂在发起调用的 assistant 消息上（design §6.3）。
    const a1 = doc.messages.find((m) => m.uuid === 'a1')
    expect(a1?.tools.map((c) => c.id)).toEqual(['t1', 't2', 't3'])
    const card1 = a1?.tools.find((c) => c.id === 't1')
    expect(card1?.result).toEqual({ isError: false, content: 'ok' })
  })

  it('结果无对应调用 → orphan 态挂在携带结果的 user 消息上', () => {
    const doc = createStreamDoc()
    doc.applyRecords([
      userMsg('u1', '问'),
      assistantMsg('a1', { text: '答' }),
      toolResultRow('u2', [{ id: 'ghost', content: '残留结果' }]),
    ])
    const st = statuses(doc)
    expect(st.get('ghost')).toBe('orphan')
    const u2 = doc.messages.find((m) => m.uuid === 'u2')
    expect(u2?.tools.map((c) => c.status)).toEqual(['orphan'])
  })

  it('重放重复批不改变配对结果（幂等）', () => {
    const doc = createStreamDoc()
    const batch: TranscriptRecord[] = [
      userMsg('u1', '问'),
      assistantMsg('a1', { toolUses: [{ id: 't1', name: 'Bash' }] }),
      toolResultRow('u2', [{ id: 't1', content: 'ok' }]),
    ]
    doc.applyRecords(batch)
    doc.applyRecords(batch)
    doc.applyRecords(batch)
    expect(doc.messages).toHaveLength(3)
    expect(statuses(doc).get('t1')).toBe('success')
  })
})

describe('缺口重放合并（seq 缺口 → 全量 refetch 语义）', () => {
  it('增量丢中间批后，权威快照按服务端顺序重排合并', () => {
    const doc = createStreamDoc()
    // 模拟缺口：只收到第 1、3 条（第 2 条丢失）。
    doc.applyRecords([userMsg('r1', '一'), userMsg('r3', '三')])
    expect(doc.messages.map((m) => m.uuid)).toEqual(['r1', 'r3'])

    // 缺口触发 /api/history 全量 refetch：权威快照含全部记录（顺序服务端定）。
    doc.applySnapshot([userMsg('r1', '一'), userMsg('r2', '二'), userMsg('r3', '三')], {
      title: '一',
      truncatedHead: false,
    })
    expect(doc.messages.map((m) => m.uuid)).toEqual(['r1', 'r2', 'r3'])
    expect(doc.messages.map((m) => m.text)).toEqual(['一', '二', '三'])
  })

  it('快照丢头（服务端 truncatedHead）时摘除快照外 uuid 并透出标记', () => {
    const doc = createStreamDoc()
    doc.applyRecords([userMsg('r1', '一'), userMsg('r2', '二'), userMsg('r3', '三')])
    doc.applySnapshot([userMsg('r2', '二'), userMsg('r3', '三')], {
      title: '二',
      truncatedHead: true,
    })
    expect(doc.messages.map((m) => m.uuid)).toEqual(['r2', 'r3'])
    expect(doc.truncatedHead).toBe(true)
    expect(doc.title).toBe('二')
  })

  it('缺口批的配对在快照合并后仍然成立', () => {
    const doc = createStreamDoc()
    doc.applyRecords([
      userMsg('u1', '问'),
      assistantMsg('a1', { toolUses: [{ id: 't1', name: 'Bash' }, { id: 't2', name: 'Read' }] }),
    ])
    // 结果行在缺口中丢失，随后由快照补齐。
    doc.applySnapshot([
      userMsg('u1', '问'),
      assistantMsg('a1', { toolUses: [{ id: 't1', name: 'Bash' }, { id: 't2', name: 'Read' }] }),
      toolResultRow('u2', [{ id: 't1', content: 'ok' }, { id: 't2', isError: true, content: 'bad' }]),
    ])
    const st = statuses(doc)
    expect(st.get('t1')).toBe('success')
    expect(st.get('t2')).toBe('failed')
  })
})

describe('busy/title/truncatedHead 透出', () => {
  it('setBusy 直写；meta 通道独立', () => {
    const doc = createStreamDoc()
    expect(doc.busy).toBe(false)
    doc.setBusy(true)
    expect(doc.busy).toBe(true)
    doc.setBusy(false)
    expect(doc.busy).toBe(false)
    expect(doc.title).toBe('')
    expect(doc.truncatedHead).toBe(false)
  })
})
