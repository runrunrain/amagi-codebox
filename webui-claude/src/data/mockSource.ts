/**
 * mockSource.ts — ?mock=1 剧本回放源（design §7.2，t7）。
 *
 * 与 HttpDataSource 同一回调面（SourceCallbacks），供无后端开发态联调视觉。
 * 剧本按 delayMs 顺序经 Transport.schedule 播放（复用 IO 缝——测试与真机
 * 行为同一条时钟路径）；stop 即取消未播事件。
 */

import {
  type DataSource,
  type HelloEvent,
  type SourceCallbacks,
  type TranscriptEvent,
  type Transport,
} from './source'

export interface MockSceneStep {
  kind: 'hello' | 'transcript' | 'busy' | 'bye'
  event: HelloEvent | TranscriptEvent | { value: boolean } | Record<string, never>
  /** 距上一步的延时（ms）。 */
  delayMs: number
}

export interface MockSourceOptions {
  transport: Transport
  callbacks: SourceCallbacks
  script?: MockSceneStep[]
}

/** 默认剧本：hello → 用户问 → 助手思考+两个工具（成功/失败）+ 用量 → 未配对工具（running）→ 收尾。 */
export const DEFAULT_MOCK_SCRIPT: MockSceneStep[] = [
  {
    kind: 'hello',
    delayMs: 0,
    event: { stream: 'mock-stream-0001', floor: 1, cursor: 0, busy: true },
  },
  {
    kind: 'transcript',
    delayMs: 300,
    event: {
      cursor: 120,
      items: [
        { kind: 'user', uuid: 'm-u1', isHuman: true, ts: '2026-11-14T10:00:00.000Z', text: '帮我看看这个仓库的构建脚本' },
      ],
    },
  },
  {
    kind: 'transcript',
    delayMs: 900,
    event: {
      cursor: 640,
      items: [
        {
          kind: 'assistant',
          uuid: 'm-a1',
          ts: '2026-11-14T10:00:02.000Z',
          thinking: '用户想了解构建脚本，我先列出仓库根目录，再读 build.sh 与 build.bat。',
          toolUses: [
            { id: 'mock-tool-1', name: 'Bash', input: { command: 'ls -la', description: '列出仓库根目录' } },
          ],
          model: 'claude-sonnet-4-6',
        },
      ],
    },
  },
  {
    kind: 'busy',
    delayMs: 200,
    event: { value: true },
  },
  {
    kind: 'transcript',
    delayMs: 1200,
    event: {
      cursor: 1180,
      items: [
        {
          kind: 'user',
          uuid: 'm-u2',
          ts: '2026-11-14T10:00:04.000Z',
          toolResults: [
            { id: 'mock-tool-1', isError: false, content: 'build.sh\nbuild.bat\npackage.json\nwails.json' },
          ],
        },
        {
          kind: 'assistant',
          uuid: 'm-a2',
          ts: '2026-11-14T10:00:06.000Z',
          text: '构建入口有两套：build.sh（macOS/Linux）与 build.bat（Windows），内部都会先构建前端再调 wails build。',
          toolUses: [
            { id: 'mock-tool-2', name: 'Read', input: { file_path: 'build.sh' } },
            { id: 'mock-tool-3', name: 'Bash', input: { command: 'node -v' } },
          ],
          usage: { in: 1234, out: 567, cacheRead: 89012, cacheWrite: 3456 },
          model: 'claude-sonnet-4-6',
        },
      ],
    },
  },
  {
    kind: 'transcript',
    delayMs: 1000,
    event: {
      cursor: 1600,
      items: [
        {
          kind: 'user',
          uuid: 'm-u3',
          ts: '2026-11-14T10:00:08.000Z',
          toolResults: [
            { id: 'mock-tool-2', isError: false, content: '#!/usr/bin/env bash\nset -euo pipefail\nnpm run build:mobile\nwails build\n' },
            { id: 'mock-tool-3', isError: true, content: 'node: no such file or directory（模拟失败态）' },
          ],
        },
        {
          kind: 'assistant',
          uuid: 'm-a3',
          ts: '2026-11-14T10:00:10.000Z',
          toolUses: [{ id: 'mock-tool-4', name: 'Bash', input: { command: 'npm --version' } }],
          model: 'claude-sonnet-4-6',
        },
      ],
    },
  },
  {
    kind: 'busy',
    delayMs: 100,
    event: { value: false },
  },
]

export class MockDataSource implements DataSource {
  private readonly transport: Transport
  private readonly cb: SourceCallbacks
  private readonly script: MockSceneStep[]
  private stopped = false
  private cancelTimer: (() => void) | null = null

  constructor(opts: MockSourceOptions) {
    this.transport = opts.transport
    this.cb = opts.callbacks
    this.script = opts.script ?? DEFAULT_MOCK_SCRIPT
  }

  start(): void {
    if (this.cancelTimer) return // 已在播放
    this.stopped = false
    this.playFrom(0)
  }

  stop(): void {
    this.stopped = true
    if (this.cancelTimer) {
      this.cancelTimer()
      this.cancelTimer = null
    }
  }

  private playFrom(index: number): void {
    if (this.stopped || index >= this.script.length) return
    const step = this.script[index]
    this.cancelTimer = this.transport.schedule(() => {
      this.cancelTimer = null
      if (this.stopped) return
      this.emit(step)
      this.playFrom(index + 1)
    }, step.delayMs)
  }

  private emit(step: MockSceneStep): void {
    switch (step.kind) {
      case 'hello':
        this.cb.onHello?.(step.event as HelloEvent)
        return
      case 'transcript':
        this.cb.onTranscript?.(step.event as TranscriptEvent)
        return
      case 'busy':
        this.cb.onBusy?.((step.event as { value: boolean }).value)
        return
      case 'bye':
        this.cb.onEnded?.()
        return
    }
  }
}
