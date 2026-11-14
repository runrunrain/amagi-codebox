/**
 * doc.ts — 消息流文档状态（design §7.2 结构、§6.3，t7）。
 *
 * 职责：
 *   - 记录按 uuid 幂等 upsert（WS 增量 / history 全量 / ?since 重放 / 缺口
 *     refetch 的重复内容统一在此合并，不产生重复消息）；
 *   - toolUses↔toolResults 按 id 配对四态：
 *       running  无配对结果（工具执行中）
 *       success  配对且 !isError
 *       failed   配对且 isError
 *       orphan   结果无对应调用（缓冲丢头后残留的 tool_result——truncatedHead
 *                场景的真实态；渲染为未知工具卡）
 *     （design §6.3 客户端配对：服务端不做配对。）
 *   - busy / title / truncatedHead 透出（事件与 history 快照两条来路）。
 *
 * 实现取舍：配对在每批 upsert 后全量重建（两遍扫描，n ≤ HistoryLimit=500，
 * 代价可忽略）——换来「任意乱序/重复到达都收敛到同一确定性结果」，
 * 幂等由构造保证而非靠调用方守序。applySnapshot 额外按快照顺序重排并
 * 摘除快照外的 uuid（服务端丢头后的权威对齐）。
 */

import { reactive } from 'vue'
import type { TranscriptRecord, UsageWire } from '../data/source'

export type ToolStatus = 'running' | 'success' | 'failed' | 'orphan'

export interface ToolCardModel {
  id: string
  name: string
  input?: unknown
  status: ToolStatus
  result?: { isError: boolean; content: string }
}

export interface StreamMessage {
  uuid: string
  kind: string
  isHuman: boolean
  ts?: string
  text?: string
  thinking?: string
  model?: string
  usage?: UsageWire
  tools: ToolCardModel[]
}

export interface StreamDocMeta {
  title?: string
  truncatedHead?: boolean
}

export interface StreamDoc {
  /** 按 uuid 幂等合并后的消息序列（reactive，视图直接渲染）。 */
  messages: StreamMessage[]
  busy: boolean
  title: string
  truncatedHead: boolean
  /** 增量批：uuid 命中则原位更新，未命中追加到尾部；配对全量重建。 */
  applyRecords(records: TranscriptRecord[]): void
  /** 权威全量快照：upsert + 按快照顺序重排 + 摘除快照外 uuid + meta 透出。 */
  applySnapshot(records: TranscriptRecord[], meta?: StreamDocMeta): void
  setBusy(value: boolean): void
}

/** 每 uuid 的合并态：视图消息 + 最新 wire 记录（配对原始材料）。 */
interface DocEntry {
  msg: StreamMessage
  rec: TranscriptRecord
}

export function createStreamDoc(): StreamDoc {
  const byUuid = new Map<string, DocEntry>()

  const doc = reactive<StreamDoc>({
    messages: [],
    busy: false,
    title: '',
    truncatedHead: false,
    applyRecords(records) {
      upsertAll(byUuid, doc, records)
      rebuildTools(byUuid, doc)
    },
    applySnapshot(records, meta) {
      upsertAll(byUuid, doc, records)
      // 权威重排：快照顺序即展示顺序；快照外 uuid（服务端丢头）摘除。
      const ordered: StreamMessage[] = []
      const seen = new Set<string>()
      for (const rec of records) {
        const entry = byUuid.get(rec.uuid)
        if (entry && !seen.has(rec.uuid)) {
          seen.add(rec.uuid)
          ordered.push(entry.msg)
        }
      }
      for (const uuid of [...byUuid.keys()]) {
        if (!seen.has(uuid)) byUuid.delete(uuid)
      }
      doc.messages.splice(0, doc.messages.length, ...ordered)
      rebuildTools(byUuid, doc)
      if (meta) {
        if (meta.title !== undefined) doc.title = meta.title
        if (meta.truncatedHead !== undefined) doc.truncatedHead = meta.truncatedHead
      }
    },
    setBusy(value: boolean) {
      doc.busy = value
    },
  })

  return doc
}

// ---------------------------------------------------------------------------
// 内部：upsert 与配对
// ---------------------------------------------------------------------------

function toMessage(rec: TranscriptRecord): StreamMessage {
  return {
    uuid: rec.uuid,
    kind: rec.kind,
    isHuman: rec.isHuman === true,
    ts: rec.ts,
    text: rec.text,
    thinking: rec.thinking,
    model: rec.model,
    usage: rec.usage,
    tools: [],
  }
}

function upsertAll(byUuid: Map<string, DocEntry>, doc: StreamDoc, records: TranscriptRecord[]): void {
  for (const rec of records) {
    if (typeof rec?.uuid !== 'string' || rec.uuid === '') continue
    const existing = byUuid.get(rec.uuid)
    if (existing) {
      // 原位更新视图字段（位置不动）；wire 记录整体替换（配对材料以最新为准）。
      const msg = existing.msg
      msg.kind = rec.kind
      msg.isHuman = rec.isHuman === true
      msg.ts = rec.ts
      msg.text = rec.text
      msg.thinking = rec.thinking
      msg.model = rec.model
      msg.usage = rec.usage
      existing.rec = rec
    } else {
      const entry: DocEntry = { msg: toMessage(rec), rec }
      byUuid.set(rec.uuid, entry)
      doc.messages.push(entry.msg)
    }
  }
}

/**
 * 全量重建工具卡配对（确定性：任何到达顺序收敛到同一结果）。
 * 第一遍按消息顺序登记 toolUse 卡（running）；第二遍按顺序落 toolResult：
 * 命中 → 标 success/failed 并附结果；未命中 → orphan 卡挂在携带结果的
 * user 消息上。
 */
function rebuildTools(byUuid: Map<string, DocEntry>, doc: StreamDoc): void {
  for (const msg of doc.messages) msg.tools = []

  const byToolId = new Map<string, ToolCardModel>()
  for (const msg of doc.messages) {
    const entry = byUuid.get(msg.uuid)
    if (!entry?.rec.toolUses) continue
    for (const tu of entry.rec.toolUses) {
      if (typeof tu?.id !== 'string' || tu.id === '') continue
      const card: ToolCardModel = { id: tu.id, name: tu.name ?? '', input: tu.input, status: 'running' }
      byToolId.set(tu.id, card)
      msg.tools.push(card)
    }
  }
  for (const msg of doc.messages) {
    const entry = byUuid.get(msg.uuid)
    if (!entry?.rec.toolResults) continue
    for (const tr of entry.rec.toolResults) {
      if (typeof tr?.id !== 'string' || tr.id === '') continue
      const card = byToolId.get(tr.id)
      if (card) {
        card.status = tr.isError === true ? 'failed' : 'success'
        card.result = { isError: tr.isError === true, content: tr.content ?? '' }
      } else {
        msg.tools.push({
          id: tr.id,
          name: '',
          status: 'orphan',
          result: { isError: tr.isError === true, content: tr.content ?? '' },
        })
      }
    }
  }
}
