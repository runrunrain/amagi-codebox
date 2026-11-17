/**
 * basePath.test.ts — resolvePlaneBase 双形态推导（design §7.2-2、§11 SPA 行、
 * R14 双 base 回归锚）。覆盖：回环根路径 / 代理子路径 / query+fragment 免疫 /
 * WS 协议 wss 换算 / 前导斜杠防御。
 */
import { describe, expect, it } from 'vitest'
import { resolvePlaneBase } from '../data/basePath'

describe('resolvePlaneBase 桌面形态（回环根路径）', () => {
  it('dir=/ 时 api 落在根', () => {
    const base = resolvePlaneBase({ protocol: 'http:', host: '127.0.0.1:51719', pathname: '/' })
    expect(base.apiBase).toBe('/')
    expect(base.api('api/info')).toBe('/api/info')
    expect(base.api('api/history')).toBe('/api/history')
  })

  it('wsUrl 为 ws:// 回环地址', () => {
    const base = resolvePlaneBase({ protocol: 'http:', host: '127.0.0.1:51719', pathname: '/' })
    expect(base.wsUrl()).toBe('ws://127.0.0.1:51719/ws/events')
  })
})

describe('resolvePlaneBase 远程形态（代理子路径 /webui/{sid}/）', () => {
  it('api 带子路径前缀', () => {
    const base = resolvePlaneBase({ protocol: 'https:', host: 'remote.example.com', pathname: '/webui/s1/' })
    expect(base.apiBase).toBe('/webui/s1/')
    expect(base.api('api/info')).toBe('/webui/s1/api/info')
    expect(base.api('ws/events')).toBe('/webui/s1/ws/events')
  })

  it('wsUrl 为 wss 且带子路径前缀', () => {
    const base = resolvePlaneBase({ protocol: 'https:', host: 'remote.example.com', pathname: '/webui/s1/' })
    expect(base.wsUrl()).toBe('wss://remote.example.com/webui/s1/ws/events')
  })

  it('深子路径同样成立（前缀逐级保留）', () => {
    const base = resolvePlaneBase({ protocol: 'https:', host: 'h.example', pathname: '/a/b/c/' })
    expect(base.api('api/info')).toBe('/a/b/c/api/info')
    expect(base.wsUrl()).toBe('wss://h.example/a/b/c/ws/events')
  })
})

describe('query 与 fragment 免疫', () => {
  it('query（?skin=light）与 fragment（#/t=）不参与推导', () => {
    // 真实 location.pathname 本就不含 query/fragment；此处同时验证防御性截断
    const base = resolvePlaneBase({
      protocol: 'https:',
      host: 'remote.example.com',
      pathname: '/webui/s1/?skin=light#/t=abc123',
    })
    expect(base.apiBase).toBe('/webui/s1/')
    expect(base.api('api/info')).toBe('/webui/s1/api/info')
    expect(base.wsUrl()).toBe('wss://remote.example.com/webui/s1/ws/events')
  })
})

describe('WS 协议换算', () => {
  it('https→wss、http→ws', () => {
    expect(
      resolvePlaneBase({ protocol: 'https:', host: 'h', pathname: '/' }).wsUrl(),
    ).toMatch(/^wss:\/\//)
    expect(
      resolvePlaneBase({ protocol: 'http:', host: 'h', pathname: '/webui/x/' }).wsUrl(),
    ).toMatch(/^ws:\/\//)
  })
})

describe('防御纵深', () => {
  it('调用侧误带前导斜杠不丢子路径前缀（禁止根绝对路径的红线兜底）', () => {
    const base = resolvePlaneBase({ protocol: 'https:', host: 'h', pathname: '/webui/s1/' })
    expect(base.api('/api/info')).toBe('/webui/s1/api/info')
  })

  it('无尾斜杠路径取最后一级目录前缀', () => {
    const base = resolvePlaneBase({ protocol: 'http:', host: 'h', pathname: '/webui/s1/index.html' })
    expect(base.apiBase).toBe('/webui/s1/')
  })
})
