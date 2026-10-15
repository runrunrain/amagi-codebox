import { afterEach, describe, expect, it, vi } from 'vitest'
import { getAIProxyState, saveAIProxySettings, startAIProxy, stopAIProxy, syncAIProxyV2rayN } from '../../api/aiProxy'
import { aiproxy } from '../../../wailsjs/go/models'

describe('native AI proxy desktop bridge', () => {
  afterEach(() => vi.unstubAllGlobals())
  it('uses the desktop facade without an extra HTTP admin server', async () => {
    const rules = [
      { id: 'claude', name: 'Claude', enabled: false, domains: ['anthropic.com'], mode: 'proxy', node: '', port: 18892 },
      { id: 'openai', name: 'OpenAI', enabled: false, domains: ['openai.com'], mode: 'proxy', node: '', port: 18893 },
      { id: 'gemini', name: 'Gemini', enabled: true, domains: ['generativelanguage.googleapis.com'], mode: 'direct', node: '', port: 0 },
    ]
    const state = { enabled: false, groups: [], settings: { routing: { proxy_port: 18890, rules } } }
    const methods = Object.fromEntries(['GetAIProxyState', 'SaveAIProxySettings', 'StartAIProxy', 'StopAIProxy', 'SyncAIProxyV2rayN'].map(name => [name, vi.fn().mockResolvedValue(state)]))
    vi.stubGlobal('window', { go: { main: { App: methods } } })
    expect(await getAIProxyState()).toEqual(state)
    await saveAIProxySettings(new aiproxy.Settings(state.settings))
    await startAIProxy(); await stopAIProxy(); await syncAIProxyV2rayN()
    for (const method of Object.values(methods)) expect(method).toHaveBeenCalledTimes(1)
    expect(methods.SaveAIProxySettings.mock.calls[0][0].routing.proxy_port).toBe(18890)
    expect(methods.SaveAIProxySettings.mock.calls[0][0].routing.rules).toEqual(rules)
  })
})
