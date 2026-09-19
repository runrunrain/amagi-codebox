import { describe, expect, it } from 'vitest'
import { sessionQuotaProvider } from '../../../components/usage/quotaModel'

/**
 * v1.3.80 回归：会话 → 额度 provider 名解析。
 *
 * 现场根因：pi 会话的 SessionInfo.Provider 是 AppType 字面量 "pi"（真实
 * provider 名在 Preset 字段），strip 拿 "pi" 探测命中后端「provider 不存在」
 * error（不落盘不留日志），条上常显「额度获取失败」。
 */
describe('sessionQuotaProvider', () => {
  it('pi/omp 会话：真实 provider 名取自 Preset 字段', () => {
    expect(sessionQuotaProvider({ appType: 'pi', provider: 'pi', preset: 'glm' })).toBe('glm')
    expect(sessionQuotaProvider({ appType: 'omp', provider: 'omp', preset: 'kimi' })).toBe('kimi')
  })

  it('pi 会话未选 provider（preset 空）→ 空串（额度不可查）', () => {
    expect(sessionQuotaProvider({ appType: 'pi', provider: 'pi', preset: '' })).toBe('')
    expect(sessionQuotaProvider({ appType: 'pi', provider: 'pi' })).toBe('')
  })

  it('codex 会话：preset 有值取该名；空则回落 codex 订阅固定键', () => {
    expect(sessionQuotaProvider({ appType: 'codex', provider: 'codex', preset: 'openai-relay' })).toBe('openai-relay')
    expect(sessionQuotaProvider({ appType: 'codex', provider: 'codex', preset: '' })).toBe('codex')
  })

  it('claudecode/opencode 会话：Provider 字段即 provider 名', () => {
    expect(sessionQuotaProvider({ appType: 'claudecode', provider: 'glm', preset: 'p1' })).toBe('glm')
    expect(sessionQuotaProvider({ appType: 'opencode', provider: 'deepseek', preset: '' })).toBe('deepseek')
  })

  it('字段缺省安全：undefined 全兜底', () => {
    expect(sessionQuotaProvider({})).toBe('')
  })
})
