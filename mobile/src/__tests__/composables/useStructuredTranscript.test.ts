import { computed } from 'vue'
import { useStructuredTranscript } from '../../composables/useStructuredTranscript'

function firstPart(transcript: ReturnType<typeof useStructuredTranscript>) {
  return transcript.turns.value[0]?.parts[0]
}

describe('useStructuredTranscript', () => {
  it('keeps empty output as an empty transcript instead of fabricating content', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-empty', appType: computed(() => 'generic') })

    transcript.appendRawChunk('')

    expect(transcript.turns.value).toEqual([])
    expect(transcript.partCount.value).toBe(0)
  })

  it('appends plain text chunks incrementally without rebuilding prior parts', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-text', appType: computed(() => 'opencode') })

    transcript.appendRawChunk('hello')
    expect(firstPart(transcript)).toMatchObject({ type: 'text', text: 'hello' })
    expect(transcript.debugStats.value).toMatchObject({ appendCalls: 1, classifiedSegments: 0 })

    transcript.appendRawChunk(' world')
    expect(firstPart(transcript)).toMatchObject({ type: 'text', text: 'hello world' })
    expect(transcript.rawText.value).toBe('hello world')
    expect(transcript.debugStats.value).toMatchObject({ appendCalls: 2, classifiedSegments: 0 })
  })

  it('classifies markdown chunks', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-markdown', appType: computed(() => 'claudecode') })

    transcript.appendRawChunk('# Plan\n\n- inspect\n- implement')

    const part = firstPart(transcript)
    expect(part).toMatchObject({ type: 'markdown' })
    expect(part && part.type === 'markdown' ? part.markdown : '').toContain('# Plan')
  })

  it('classifies tool-like output after segment boundary', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-tool', appType: computed(() => 'claudecode') })

    transcript.appendRawChunk('Read src/main.ts\nLoaded 20 lines\n\n')

    const part = firstPart(transcript)
    expect(part).toMatchObject({
      type: 'tool',
      name: 'Read',
      title: 'Read src/main.ts',
      outputPreview: 'Loaded 20 lines',
    })
  })

  it('classifies unified diff output and counts changes', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-diff', appType: computed(() => 'codex') })

    transcript.appendRawChunk('diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new')

    const part = firstPart(transcript)
    expect(part).toMatchObject({
      type: 'diff',
      filename: 'a.txt',
      additions: 1,
      deletions: 1,
    })
  })

  it('keeps raw text clean for ANSI/TUI chunks', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-raw', appType: computed(() => 'opencode') })

    transcript.appendRawChunk('\u001B[32mgreen\u001B[0m\n╭─ panel')

    expect(transcript.rawText.value).toBe('green\n   panel')
    expect(transcript.rawText.value).not.toContain('\u001B[32m')
  })

  it('handles repeated snapshots by appending only the delta', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-snapshot', appType: computed(() => 'generic') })

    transcript.ingestRawSnapshot('first')
    transcript.ingestRawSnapshot('first')
    transcript.ingestRawSnapshot('first second')

    expect(transcript.turns.value[0]?.parts).toHaveLength(1)
    expect(firstPart(transcript)).toMatchObject({ type: 'text', text: 'first second' })
    expect(transcript.debugStats.value).toMatchObject({ appendCalls: 2, snapshotResets: 0 })
  })

  it('bounds raw text and visible parts for long incremental output', () => {
    const transcript = useStructuredTranscript({
      sessionId: 's-long',
      appType: computed(() => 'generic'),
      maxRawChars: 60,
      maxLines: 4,
      maxParts: 3,
    })

    for (let i = 0; i < 8; i += 1) {
      transcript.appendRawChunk(`segment ${i}\n\n`)
    }

    expect(transcript.rawText.value.length).toBeLessThanOrEqual(60)
    expect(transcript.rawText.value.split('\n').length).toBeLessThanOrEqual(4)
    expect(transcript.turns.value[0]?.parts).toHaveLength(3)
    expect(transcript.turns.value[0]?.parts.map((part) => part.type)).toEqual(['text', 'text', 'text'])
    expect(transcript.debugStats.value).toMatchObject({ appendCalls: 8, classifiedSegments: 8, retainedParts: 3 })
  })

  it('appends backend structured parts without reclassifying raw text', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-structured', appType: computed(() => 'generic') })

    transcript.appendStructuredPart({
      id: 'pty-1-markdown',
      type: 'markdown',
      markdown: '# Backend Part',
      source: { kind: 'pty', seqStart: 1, seqEnd: 1 },
      createdAt: '2026-05-27T00:00:00.000Z',
    }, { rawChunk: '# Backend Part\r\n' })

    expect(firstPart(transcript)).toMatchObject({ type: 'markdown', markdown: '# Backend Part' })
    expect(transcript.rawText.value).toBe('# Backend Part\n')
    expect(transcript.debugStats.value).toMatchObject({ appendCalls: 1, classifiedSegments: 0, structuredParts: 1 })
  })

  it('preserves pending legacy output without a delimiter when a structured part arrives', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-legacy-mixed', appType: computed(() => 'generic') })

    transcript.appendRawChunk('legacy history output without delimiter')
    transcript.appendStructuredPart({
      id: 'pty-10-text',
      type: 'text',
      text: 'new structured output',
      source: { kind: 'pty', seqStart: 10, seqEnd: 10 },
      createdAt: '2026-05-27T00:00:00.000Z',
    }, { rawChunk: 'new structured output' })

    const parts = transcript.turns.value[0]?.parts ?? []
    expect(parts).toHaveLength(2)
    expect(parts[0]).toMatchObject({ type: 'text', text: 'legacy history output without delimiter' })
    expect(parts[1]).toMatchObject({ type: 'text', text: 'new structured output' })
    expect(parts.filter((part) => part.type === 'text' && part.text === 'legacy history output without delimiter')).toHaveLength(1)
    expect(parts.filter((part) => part.type === 'text' && part.text === 'new structured output')).toHaveLength(1)
    expect(transcript.rawText.value).toBe('legacy history output without delimiternew structured output')
    expect(transcript.debugStats.value).toMatchObject({ appendCalls: 2, classifiedSegments: 1, structuredParts: 1 })
  })

  it('preserves timeout fallback raw output when a later structured part arrives', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-timeout-mixed', appType: computed(() => 'generic') })

    transcript.appendRawChunk('timeout fallback output still pending', { source: 'fallback' })
    transcript.appendStructuredPart({
      id: 'pty-11-markdown',
      type: 'markdown',
      markdown: '## Subsequent structured output',
      source: { kind: 'pty', seqStart: 11, seqEnd: 11 },
      createdAt: '2026-05-27T00:00:00.000Z',
    }, { rawChunk: '## Subsequent structured output' })

    const parts = transcript.turns.value[0]?.parts ?? []
    expect(parts).toHaveLength(2)
    expect(parts[0]).toMatchObject({ type: 'text', text: 'timeout fallback output still pending' })
    expect(parts[1]).toMatchObject({ type: 'markdown', markdown: '## Subsequent structured output' })
    expect(parts.filter((part) => part.type === 'text' && part.text === 'timeout fallback output still pending')).toHaveLength(1)
    expect(parts.filter((part) => part.type === 'markdown' && part.markdown === '## Subsequent structured output')).toHaveLength(1)
    expect(transcript.rawText.value).toBe('timeout fallback output still pending## Subsequent structured output')
    expect(transcript.debugStats.value).toMatchObject({ appendCalls: 2, classifiedSegments: 1, structuredParts: 1 })
  })

  it('maps backend raw terminal reason into transcript parts', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-structured-raw', appType: computed(() => 'opencode') })

    transcript.appendStructuredPart({
      id: 'pty-2-raw-terminal',
      type: 'raw-terminal',
      raw: { text: '\u001B[32mgreen\u001B[0m', reason: 'ansi' },
      source: { kind: 'pty', seqStart: 2, seqEnd: 2 },
      createdAt: '2026-05-27T00:00:00.000Z',
    })

    expect(firstPart(transcript)).toMatchObject({ type: 'diagnostic-ref', reason: 'ansi' })
    expect(transcript.rawText.value).toBe('green')
  })

  it('updates duplicate structured part ids instead of appending duplicate cards', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-dedup', appType: computed(() => 'generic') })
    const base = {
      id: 'dup-part-001',
      type: 'tool' as const,
      source: { kind: 'pty' as const, seqStart: 1, seqEnd: 1 },
      createdAt: '2026-05-27T00:00:00.000Z',
    }

    transcript.appendStructuredPart({
      ...base,
      tool: { name: 'Read', state: 'running', title: 'Read old.ts' },
    })
    transcript.appendStructuredPart({
      ...base,
      tool: { name: 'Read', state: 'completed', title: 'Read new.ts' },
    })

    const parts = transcript.turns.value[0]?.parts ?? []
    expect(parts.filter((part) => part.id === 'dup-part-001')).toHaveLength(1)
    expect(parts[0]).toMatchObject({ type: 'tool', title: 'Read new.ts', state: 'completed' })
  })

  it('isolates object-like raw chunks into bounded diagnostics', () => {
    const transcript = useStructuredTranscript({ sessionId: 's-object', appType: computed(() => 'generic') })
    const payload = JSON.stringify({ token: 'sk-1234567890abcdef', nested: { key: 'value' } })

    transcript.appendRawChunk(payload)

    expect(transcript.rawText.value).toBe('')
    expect(firstPart(transcript)).toMatchObject({ type: 'diagnostic-ref', reason: 'object-payload' })
    expect(transcript.diagnostics.value[0]?.preview).toContain('sk-[REDACTED]')
    expect(transcript.diagnostics.value[0]?.preview.length).toBeLessThanOrEqual(801)
  })
})
