/**
 * 远程终端传输适配器（RC2-5 · T0-2 结论 B 的远程实现）
 *
 * 把 /ws/v1 远程终端会话接到 useTerminalEngine 渲染内核：
 *   - 输出/退出：store 的 rc:* 事件分发（Go 侧 conn.go 已完成 attach 历史回放、
 *     seq 去重、断线 backfill 补洞；前端按事件续渲染，不做二次排序）；
 *   - 输入：RemoteClientTerminalSendInput 绑定（UTF-8 文本，Go 侧 base64 +
 *     outbox 幂等）；
 *   - resize：RemoteClientTerminalResize 绑定（仅 attached/degraded 发送，
 *     其余状态静默丢弃，重连 attached 后由视图强制 fit 补齐）；
 *   - 历史：无快照拉取（fetchHistory → null）——Go 侧 attach 回放经输出事件
 *     流入，引擎的 liveBuffer/历史屏障自然兼容；
 *   - history.gap：缺口通知合成「虚线分隔提示条」写入终端输出区
 *     （契约为服务端权威裁定，如实展示，不吞不改）；
 *   - 剪贴板图片：远程不支持（本机落盘路径对远端无意义），返回空串。
 */

import type { TerminalTransport } from './useTerminalEngine';
import { uint8ToBase64 } from './useTerminalEngine';
import { sendRemoteTerminalInput, resizeRemoteTerminal } from '../api/remoteClient';
import { useRemoteClientStore } from '../stores/remoteClient';

/** 远程会话视为「进程已终结」的会话态（映射为引擎退出标记）。 */
const TERMINAL_STATES = new Set(['exited', 'stopped', 'removed']);

function gapNoticeLine(fromSeq: number, toSeq: number, source: string): string {
  const rule = '─ ─ '.repeat(12);
  const src = source ? `（${source}）` : '';
  return (
    `\r\n\x1b[90m${rule}\x1b[0m\r\n` +
    `\x1b[90m[远程终端] 历史缺口 seq ${fromSeq}–${toSeq}${src}：该区间输出不可恢复\x1b[0m\r\n` +
    `\x1b[90m${rule}\x1b[0m\r\n`
  );
}

export function createRemoteTerminalTransport(sessionId: string): TerminalTransport {
  const store = useRemoteClientStore();

  return {
    subscribeOutput(cb) {
      return store.subscribeRemoteTerminalOutput(sessionId, (ev) => {
        if (ev.kind === 'output') {
          cb({ seq: ev.seq, data: ev.data });
        } else {
          // gap 提示条：seq=0 走直播路径（不参与去重），保持到达顺序。
          const bytes = new TextEncoder().encode(gapNoticeLine(ev.fromSeq, ev.toSeq, ev.source));
          cb({ seq: 0, data: uint8ToBase64(bytes) });
        }
      });
    },
    subscribeExit(cb) {
      // 远程无 pty:exit 等价事件：以 rc:session-state 终态映射退出标记；
      // 同一终态只通知一次（attach 快照态 + 后续事件可能重复）。
      let signaled = '';
      return store.subscribeRemoteSessionState(sessionId, (ev) => {
        if (!TERMINAL_STATES.has(ev.state) || signaled === ev.state) return;
        signaled = ev.state;
        cb({});
      });
    },
    // 历史由 Go 侧 attach 回放经输出事件流入，无快照拉取。
    fetchHistory: () => Promise.resolve(null),
    write(text) {
      return sendRemoteTerminalInput(sessionId, text);
    },
    // 剪贴板图片保存是本机落盘能力，远程路径无意义：不支持（返回空串）。
    saveClipboardImage: () => Promise.resolve(''),
    async resize(cols, rows) {
      // 仅 attached/degraded 可 resize（Go 侧拒绝其余状态）；断开/只读期间
      // 静默丢弃，避免引擎 resize 重试风暴——重连 attached 后视图强制 fit 补齐。
      const st = store.remoteTerminalStates[sessionId]?.connState;
      if (st !== 'attached' && st !== 'degraded') return;
      await resizeRemoteTerminal(sessionId, cols, rows);
    },
  };
}
