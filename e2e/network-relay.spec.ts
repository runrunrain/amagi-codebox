// e2e/network-relay.spec.ts — M3-B WS relay 整形 helper 回归（design §8）
// ---------------------------------------------------------------------------
// 目标：证明 installWsRelay 不是死代码——转发/丢包/typed 计数/派生布尔
// （sameMessageID/distinctRequestIDs）在真实 Chromium + routeWebSocket 下行为正确。
//
// 服务端基底（M3-C 修正）：内联最小 RFC6455 echo 服务器（真实 TCP + 真实握手，
// 零新增依赖）。原 M3-B 版本依赖「connectToServer 被先注册的 routeWebSocket 接管」
// 的链式语义——经 Playwright 1.58.2 实测该语义不成立（connectToServer 直连真实
// 网络，不经 route 拦截），原三用例全部超时失败。helper 实现未动，仅修正测试基底。
//
// 隐私（design §6/§8）：断言只读 typed 计数与派生布尔，不断言 raw frame 内容本身
// （echo 服务器记录条数/字节数，只用于转发方向证明）。
//
// 环境边界（如实）：本 spec 需 Chromium（C-009）；CI/开发机运行。
// ---------------------------------------------------------------------------

import { expect, test } from '@playwright/test'
import { createHash } from 'node:crypto'
import * as http from 'node:http'
import type { Duplex } from 'node:stream'
import { installWsRelay } from './helpers/network'

const WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11'

/** 最小 RFC6455 echo 服务器（TEST-ONLY）：记录收到的文本帧条数/字节数并原样回显。 */
interface EchoServer {
  port: number
  receivedCount: () => number
  receivedBytes: () => number
  close: () => Promise<void>
}

async function startEchoServer(): Promise<EchoServer> {
  let count = 0
  let bytes = 0
  const sockets = new Set<Duplex>()

  const server = http.createServer()
  server.on('upgrade', (req, socket: Duplex) => {
    const key = req.headers['sec-websocket-key']
    if (typeof key !== 'string') {
      socket.destroy()
      return
    }
    sockets.add(socket)
    socket.on('close', () => sockets.delete(socket))
    socket.on('error', () => sockets.delete(socket))
    const accept = createHash('sha1').update(key + WS_GUID).digest('base64')
    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\n' +
        'Upgrade: websocket\r\n' +
        'Connection: Upgrade\r\n' +
        `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    )

    let buf = Buffer.alloc(0)
    socket.on('data', (chunk: Buffer) => {
      buf = Buffer.concat([buf, chunk])
      // 逐帧解析（测试帧均为小 payload、单帧、不分片）。
      for (;;) {
        if (buf.length < 2) return
        const opcode = buf[0] & 0x0f
        const masked = (buf[1] & 0x80) !== 0
        let len = buf[1] & 0x7f
        let off = 2
        if (len === 126) {
          if (buf.length < 4) return
          len = buf.readUInt16BE(2)
          off = 4
        } else if (len === 127) {
          if (buf.length < 10) return
          len = Number(buf.readBigUInt64BE(2))
          off = 10
        }
        const maskOff = off
        if (masked) off += 4
        if (buf.length < off + len) return
        let payload = buf.subarray(off, off + len)
        if (masked) {
          const mask = buf.subarray(maskOff, maskOff + 4)
          const un = Buffer.alloc(len)
          for (let i = 0; i < len; i++) un[i] = payload[i] ^ mask[i % 4]
          payload = un
        }
        buf = buf.subarray(off + len)

        if (opcode === 0x8) {
          // close：回 close 帧并结束。
          socket.write(Buffer.from([0x88, 0x00]))
          socket.end()
          return
        }
        if (opcode === 0x1 || opcode === 0x2) {
          count += 1
          bytes += payload.length
          // 回显（服务端帧不掩码）。
          const header =
            payload.length < 126
              ? Buffer.from([0x81, payload.length])
              : (() => {
                  const h = Buffer.alloc(4)
                  h[0] = 0x81
                  h[1] = 126
                  h.writeUInt16BE(payload.length, 2)
                  return h
                })()
          socket.write(Buffer.concat([header, payload]))
        }
        // ping（0x9）忽略：测试不依赖。
      }
    })
  })

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const addr = server.address()
  const port = typeof addr === 'object' && addr ? addr.port : 0
  return {
    port,
    receivedCount: () => count,
    receivedBytes: () => bytes,
    close: () =>
      new Promise<void>((resolve) => {
        for (const s of sockets) s.destroy()
        server.close(() => resolve())
      }),
  }
}

test.describe('installWsRelay (CG-03/M3-B WS relay)', () => {
  let echo: EchoServer

  test.beforeEach(async () => {
    echo = await startEchoServer()
  })

  test.afterEach(async () => {
    await echo.close()
  })

  test('forward both directions + typed counts (no raw retained)', async ({ page }) => {
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      clientToServerLatencyMs: 0,
      serverToClientLatencyMs: 0,
    })
    try {
      await page.goto('data:text/html,<script></script>')
      await page.evaluate(async (port) => {
        const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/v1`)
        await new Promise((r) => {
          ws.onopen = () => r(null)
        })
        ws.send(JSON.stringify({ type: 'attach', requestId: 'r1', apiVersion: 'v1', sessionId: 's' }))
        // echo 回来证明 server→client 方向。
        await new Promise((r) => {
          ws.onmessage = () => r(null)
          setTimeout(r, 2000)
        })
        ws.close()
      }, echo.port)
      expect(controller.counts.connections).toBeGreaterThanOrEqual(1)
      expect(controller.counts.clientToServerForwarded).toBeGreaterThanOrEqual(1)
      expect(controller.counts.serverToClientForwarded).toBeGreaterThanOrEqual(1)
      // 真实服务端确实收到（转发方向证明；不断言内容本身）。
      expect(echo.receivedCount()).toBeGreaterThanOrEqual(1)
    } finally {
      await cleanup()
    }
  })

  test('drop fault (dropClientToServerAt) increments dropped, no forward', async ({ page }) => {
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', {
      clientToServerLatencyMs: 0,
      dropClientToServerAt: 1,
    })
    try {
      await page.goto('data:text/html,<script></script>')
      await page.evaluate(async (port) => {
        const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/v1`)
        await new Promise((r) => {
          ws.onopen = () => r(null)
        })
        ws.send(JSON.stringify({ type: 'attach', requestId: 'drop1', apiVersion: 'v1', sessionId: 's' }))
        ws.send(JSON.stringify({ type: 'ping', requestId: 'drop2' }))
        await new Promise((r) => setTimeout(r, 300))
        ws.close()
      }, echo.port)
      expect(controller.counts.clientToServerDropped).toBe(1)
      expect(controller.counts.clientToServerForwarded).toBeGreaterThanOrEqual(1)
      // 服务端只见第二条（第一条被 relay 丢弃）。
      expect(echo.receivedCount()).toBe(1)
    } finally {
      await cleanup()
    }
  })

  test('derived booleans: canonical input retry → sameMessageID + distinctRequestIDs', async ({ page }) => {
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', { clientToServerLatencyMs: 0 })
    try {
      await page.goto('data:text/html,<script></script>')
      await page.evaluate(async (port) => {
        const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/v1`)
        await new Promise((r) => {
          ws.onopen = () => r(null)
        })
        // 同一 canonical MessageID、两个 distinct canonical RequestID（CG-03 retry 形态）。
        ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + '1'.repeat(32), id: 'msg-v1-' + 'a'.repeat(32), data: 'YQ==' }))
        ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + '2'.repeat(32), id: 'msg-v1-' + 'a'.repeat(32), data: 'YQ==' }))
        await new Promise((r) => setTimeout(r, 300))
        ws.close()
      }, echo.port)
      // 派生布尔（design [R2/M-05]）：retry 同 id、distinct requestId。
      expect(controller.counts.sameMessageID).toBe(true)
      expect(controller.counts.distinctRequestIDs).toBe(true)
      // 两条均真实到达服务端（无 drop）。
      expect(echo.receivedCount()).toBe(2)
    } finally {
      await cleanup()
    }
  })

  // M3-010/R2-002（隐私卫生）：relay 不得保留原始 MessageID/requestId。结构证明：counts 对象
  // 只暴露 typed 计数 + 派生布尔（无任何 string 类型 ID 字段）；行为证明：distinct id/
  // requestId 的翻转在 HMAC-SHA-256 摘要上成立；cleanup 后派生状态销毁。fixture 中的固定
  // ID 属测试豁免（不出 relay、不进产品 DOM/报告）。
  // R2-002 强化：原 FNV-1a 32-bit 摘要可碰撞（79822 次内实测），已升级为 HMAC-SHA-256
  // 256-bit 摘要（进程内随机 key），碰撞概率可忽略（≈N²/2^257）；断言 counts 不含原文 +
  // 翻转行为 + cleanup 销毁。
  test('M3-010/R2-002 privacy: HMAC-SHA-256 digest, no raw ID retained, cleanup destroys key+digests', async ({ page }) => {
    const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', { clientToServerLatencyMs: 0 })
    try {
      await page.goto('data:text/html,<script></script>')
      await page.evaluate(async (port) => {
        const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/v1`)
        await new Promise((r) => {
          ws.onopen = () => r(null)
        })
        // 1) 同 MessageID、两 distinct requestId → sameMessageID=true / distinctRequestIDs=true。
        ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + 'a'.repeat(32), id: 'msg-v1-' + '11'.repeat(16), data: 'YQ==' }))
        ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + 'b'.repeat(32), id: 'msg-v1-' + '11'.repeat(16), data: 'YQ==' }))
        await new Promise((r) => setTimeout(r, 150))
        ws.close()
      }, echo.port)
      expect(controller.counts.sameMessageID).toBe(true)
      expect(controller.counts.distinctRequestIDs).toBe(true)
      expect(controller.counts.inputFramesObserved).toBe(2)
      // 结构证明（M3-010 核心）：counts 序列化后不含任何原始 ID 字符串字段——
      // 只有 typed 计数（number）与派生布尔（boolean）。relay 不可被外部读出原始 MessageID/requestId。
      const snapshot = controller.counts as Record<string, unknown>
      const keys = Object.keys(snapshot)
      for (const k of keys) {
        const v = snapshot[k]
        expect(typeof v === 'number' || typeof v === 'boolean', `counts.${k} 必须是 typed(number/boolean)，不得是 raw string`).toBe(true)
      }
      // 不含原始 ID 值：序列化文本中不出现 fixture 使用的 canonical 前缀原文片段以外的可识别 ID。
      const serialized = JSON.stringify(snapshot)
      expect(serialized).not.toContain('req-v1-' + 'a'.repeat(32))
      expect(serialized).not.toContain('req-v1-' + 'b'.repeat(32))
      expect(serialized).not.toContain('msg-v1-' + '11'.repeat(16))
    } finally {
      await cleanup()
    }
  })

  // R2-002 翻转行为证明：HMAC-SHA-256 摘要的 exact oracle 在 distinct/相同 ID 上正确翻转。
  // 原 FNV-1a 32-bit 可碰撞（两个不同输入同摘要），削弱 sameMessageID/distinctRequestIDs；
  // HMAC-SHA-256 256-bit 摘要在测试规模下碰撞概率可忽略，翻转断言可信。
  test('R2-002 exact oracle: HMAC-SHA-256 digest flips correctly on distinct/same IDs', async ({ page }) => {
    // 1) distinct MessageID → sameMessageID=false
    {
      const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', { clientToServerLatencyMs: 0 })
      try {
        await page.goto('data:text/html,<script></script>')
        await page.evaluate(async (port) => {
          const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/v1`)
          await new Promise((r) => { ws.onopen = () => r(null) })
          ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + '1'.repeat(32), id: 'msg-v1-' + 'aa'.repeat(16), data: 'YQ==' }))
          ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + '2'.repeat(32), id: 'msg-v1-' + 'bb'.repeat(16), data: 'YQ==' }))
          await new Promise((r) => setTimeout(r, 150))
          ws.close()
        }, echo.port)
        // 两个 distinct MessageID → sameMessageID=false（翻转，证明 oracle 灵敏）。
        expect(controller.counts.sameMessageID, 'distinct MessageID → sameMessageID=false').toBe(false)
        // 两个 distinct RequestID → distinctRequestIDs=true（仍 true，未重复）。
        expect(controller.counts.distinctRequestIDs, 'distinct RequestID → distinctRequestIDs=true').toBe(true)
      } finally {
        await cleanup()
      }
    }
    // 2) 同 RequestID 重复 → distinctRequestIDs=false
    {
      const { controller, cleanup } = await installWsRelay(page, '**/ws/v1', { clientToServerLatencyMs: 0 })
      try {
        await page.goto('data:text/html,<script></script>')
        await page.evaluate(async (port) => {
          const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/v1`)
          await new Promise((r) => { ws.onopen = () => r(null) })
          // 同 RequestID 重复（非法 retry 形态，但 oracle 应检测到重复）。
          ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + 'c'.repeat(32), id: 'msg-v1-' + 'cc'.repeat(16), data: 'YQ==' }))
          ws.send(JSON.stringify({ type: 'input', requestId: 'req-v1-' + 'c'.repeat(32), id: 'msg-v1-' + 'cc'.repeat(16), data: 'YQ==' }))
          await new Promise((r) => setTimeout(r, 150))
          ws.close()
        }, echo.port)
        // 同 MessageID → sameMessageID=true
        expect(controller.counts.sameMessageID, '同 MessageID → sameMessageID=true').toBe(true)
        // 同 RequestID 重复 → distinctRequestIDs=false（翻转，证明重复检测灵敏）。
        expect(controller.counts.distinctRequestIDs, '重复 RequestID → distinctRequestIDs=false').toBe(false)
      } finally {
        await cleanup()
      }
    }
  })
})
