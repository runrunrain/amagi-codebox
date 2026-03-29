import { beforeEach, describe, expect, it, vi } from 'vitest'

const getSessions = vi.fn()

vi.mock('../../api/client', () => ({
  apiClient: {
    getSessions,
  },
}))

describe('useSessionMetadata', () => {
  beforeEach(() => {
    getSessions.mockReset()
  })

  it('maps session metadata using explicit appType when available', async () => {
    getSessions.mockResolvedValue([
      {
        id: 'abc',
        appType: 'opencode',
        mode: 'embedded',
        status: 'running',
        provider: 'OpenAI',
        preset: '',
        model: 'gpt-5',
        workDir: '/workspace',
        startedAt: '',
        pid: 1,
      },
    ])

    const { fetchSessionMetadata } = await import('../../composables/useSessionMetadata')
    await expect(fetchSessionMetadata('abc')).resolves.toEqual({
      sessionId: 'abc',
      appType: 'opencode',
      provider: 'OpenAI',
      model: 'gpt-5',
      mode: 'embedded',
      status: 'running',
      workDir: '/workspace',
    })
  })

  it('falls back to mode when appType is empty', async () => {
    getSessions.mockResolvedValue([
      {
        id: 'def',
        appType: '',
        mode: 'claude',
        status: 'running',
        provider: 'Claude',
        preset: 'opus',
        model: '',
        workDir: '/repo',
        startedAt: '',
        pid: 2,
      },
    ])

    const { fetchSessionMetadata } = await import('../../composables/useSessionMetadata')
    await expect(fetchSessionMetadata('def')).resolves.toEqual({
      sessionId: 'def',
      appType: 'claudecode',
      provider: 'Claude',
      model: '',
      mode: 'claude',
      status: 'running',
      workDir: '/repo',
    })
  })

  it('returns null when session is missing', async () => {
    getSessions.mockResolvedValue([])

    const { fetchSessionMetadata } = await import('../../composables/useSessionMetadata')
    await expect(fetchSessionMetadata('missing')).resolves.toBeNull()
  })
})
