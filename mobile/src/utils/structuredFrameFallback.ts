export type StructuredFallbackTimer = ReturnType<typeof setTimeout>

export interface StructuredFallbackEntry {
  text: string
  timer: StructuredFallbackTimer
}

export class StructuredFrameFallbacks {
  private readonly entries = new Map<number, StructuredFallbackEntry>()
  private readonly resolvedSeqs = new Set<number>()
  private readonly maxResolvedSeqs = 512
  private readonly timeoutMs: number
  private readonly onFallback: (seq: number, text: string) => void

  constructor(
    timeoutMs: number,
    onFallback: (seq: number, text: string) => void,
  ) {
    this.timeoutMs = timeoutMs
    this.onFallback = onFallback
  }

  schedule(seq: number, text: string) {
    const existing = this.entries.get(seq)
    if (existing) {
      clearTimeout(existing.timer)
    }

    const timer = setTimeout(() => {
      this.appendFallback(seq)
    }, this.timeoutMs)
    this.entries.set(seq, { text, timer })
  }

  consume(seq: number): string | undefined {
    const pending = this.entries.get(seq)
    if (!pending) return undefined

    clearTimeout(pending.timer)
    this.entries.delete(seq)
    this.markResolved(seq)
    return pending.text
  }

  hasResolved(seq: number): boolean {
    return this.resolvedSeqs.has(seq)
  }

  flush() {
    for (const seq of Array.from(this.entries.keys())) {
      this.appendFallback(seq)
    }
  }

  clear() {
    for (const entry of this.entries.values()) {
      clearTimeout(entry.timer)
    }
    this.entries.clear()
    this.resolvedSeqs.clear()
  }

  size() {
    return this.entries.size
  }

  private appendFallback(seq: number) {
    const pending = this.entries.get(seq)
    if (!pending) return

    clearTimeout(pending.timer)
    this.entries.delete(seq)
    this.markResolved(seq)
    this.onFallback(seq, pending.text)
  }

  private markResolved(seq: number) {
    this.resolvedSeqs.add(seq)
    if (this.resolvedSeqs.size <= this.maxResolvedSeqs) return

    const first = this.resolvedSeqs.values().next().value
    if (typeof first === 'number') {
      this.resolvedSeqs.delete(first)
    }
  }
}
