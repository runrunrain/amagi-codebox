/**
 * draft.ts — /api/draft 草稿同步（design §6.1、§7.2，t8）。
 *
 * 契约（internal/claudewebui/server.go handleDraftGet / handleDraftPut）：
 *   - GET /api/draft → {v:1, draft:string}（挂载恢复）；
 *   - PUT /api/draft {draft}（≤128KiB，空串即清除；draft 不写 PTY、无车道概念）。
 *
 * 为什么草稿走服务端内存：sandbox iframe（无 allow-same-origin）拿不到
 * localStorage，且宿主 url watch 会整页重载 iframe——pi 实证需求（design §5.2）。
 *
 * 一切错误静默容错（design §7.2 明确）：网络异常、非 2xx 一律吞掉，含远程
 * 未持控设备经反代写面门的 403 control.forbidden（PUT /api/draft 在写面集合
 * webuiBackendWriteFaces 内——与 pi 语义一致）。本地输入回显不受影响；恢复
 * 持控后下一次变更的防抖写自动把最新文本重写到服务端，无需补偿逻辑。
 *
 * 红线：URL 一律经 resolvePlaneBase 推导（双 base 硬约束，禁止根绝对路径
 * 与硬编码 127.0.0.1）；请求带 Bearer。防抖经 Transport.schedule（IO 缝——
 * 测试与真机走同一条时钟路径）。
 */

import { resolvePlaneBase, type PlaneLocation } from '../data/basePath'
import type { Transport } from '../data/source'

/** PUT 防抖窗口（t8 任务卡：500ms）。 */
export const DRAFT_DEBOUNCE_MS = 500

export interface DraftSync {
  /** GET /api/draft；任何失败静默返回 ''（绝不抛错）。 */
  load(): Promise<string>
  /** 防抖 500ms 后 PUT；窗口内的连续变更合并为最后一次文本。 */
  schedule(text: string): void
  /** 取消尚未发出的防抖写（组件卸载时调用）。 */
  dispose(): void
}

export interface DraftSyncOptions {
  transport: Transport
  loc: PlaneLocation
  /** token 取值器（非值捕获）：宿主 token 在父组件 mounted 才就绪，见 composerState.ComposerDeps。 */
  token(): string
}

export function createDraftSync(opts: DraftSyncOptions): DraftSync {
  const base = resolvePlaneBase(opts.loc)

  let pending: string | null = null
  let cancelTimer: (() => void) | null = null
  let disposed = false

  function authHeaders(): Record<string, string> {
    return { Authorization: `Bearer ${opts.token()}` }
  }

  /** 静默 PUT：状态码不看（非 2xx 含 403 写面门拒绝一律忽略），异常吞掉。 */
  async function put(text: string): Promise<void> {
    try {
      await opts.transport.fetchJson(base.api('api/draft'), {
        method: 'PUT',
        headers: { ...authHeaders(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ draft: text }),
      })
    } catch {
      /* 静默容错：草稿是尽力而为的暂存，不允许打断输入体验 */
    }
  }

  return {
    async load(): Promise<string> {
      try {
        const res = await opts.transport.fetchJson(base.api('api/draft'), {
          headers: authHeaders(),
        })
        const body = res.body as { v?: number; draft?: unknown } | null
        if (res.status !== 200 || !body || body.v !== 1) return ''
        return typeof body.draft === 'string' ? body.draft : ''
      } catch {
        return ''
      }
    },

    schedule(text: string): void {
      if (disposed) return
      pending = text
      if (cancelTimer) {
        cancelTimer()
        cancelTimer = null
      }
      cancelTimer = opts.transport.schedule(() => {
        cancelTimer = null
        if (pending === null) return
        const latest = pending
        pending = null
        void put(latest)
      }, DRAFT_DEBOUNCE_MS)
    },

    dispose(): void {
      disposed = true
      pending = null
      if (cancelTimer) {
        cancelTimer()
        cancelTimer = null
      }
    },
  }
}
