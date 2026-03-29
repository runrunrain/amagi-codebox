export interface UseOutputBufferOptions {
  onFlush: (data: Uint8Array) => void
  flushInterval?: number
}

export function useOutputBuffer(options: UseOutputBufferOptions) {
  const { onFlush, flushInterval = 80 } = options

  let outputBuffer: Uint8Array[] = []
  let flushTimer: ReturnType<typeof setTimeout> | null = null

  function flush() {
    flushTimer = null
    if (outputBuffer.length === 0) return

    const totalLength = outputBuffer.reduce((sum, arr) => sum + arr.length, 0)
    const merged = new Uint8Array(totalLength)
    let offset = 0
    for (const chunk of outputBuffer) {
      merged.set(chunk, offset)
      offset += chunk.length
    }

    outputBuffer = []
    onFlush(merged)
  }

  function bufferOutput(data: Uint8Array) {
    outputBuffer.push(data)
    if (!flushTimer) {
      flushTimer = setTimeout(flush, flushInterval)
    }
  }

  function flushNow() {
    if (flushTimer) {
      clearTimeout(flushTimer)
      flushTimer = null
    }
    flush()
  }

  function dispose() {
    if (flushTimer) {
      clearTimeout(flushTimer)
      flushTimer = null
    }
    outputBuffer = []
  }

  return {
    bufferOutput,
    flushNow,
    dispose,
  }
}
