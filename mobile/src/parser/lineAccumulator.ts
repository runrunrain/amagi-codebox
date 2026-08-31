export interface LineAccumulatorSnapshot {
  activeLine: string
}

export interface LineAccumulator {
  push(chunk: string): string[]
  flush(): string[]
  reset(): void
  snapshot(): LineAccumulatorSnapshot
}

export function createLineAccumulator(): LineAccumulator {
  let activeLine = ''
  let pendingCR = false

  function push(chunk: string): string[] {
    const completedLines: string[] = []
    let i = 0

    if (pendingCR) {
      pendingCR = false
      if (chunk.length > 0 && chunk[0] === '\n') {
        completedLines.push(activeLine)
        activeLine = ''
        i = 1
      } else {
        activeLine = ''
      }
    }

    for (; i < chunk.length; i++) {
      const char = chunk[i]

      if (char === '\r') {
        if (i + 1 < chunk.length) {
          if (chunk[i + 1] === '\n') {
            completedLines.push(activeLine)
            activeLine = ''
            i++
          } else {
            activeLine = ''
          }
        } else {
          pendingCR = true
        }
        continue
      }

      if (char === '\n') {
        completedLines.push(activeLine)
        activeLine = ''
        continue
      }

      activeLine += char
    }

    return completedLines
  }

  function flush(): string[] {
    if (pendingCR) {
      activeLine = ''
      pendingCR = false
    }
    if (!activeLine) return []
    const lines = [activeLine]
    activeLine = ''
    return lines
  }

  function reset() {
    activeLine = ''
    pendingCR = false
  }

  function snapshot(): LineAccumulatorSnapshot {
    return { activeLine }
  }

  return {
    push,
    flush,
    reset,
    snapshot,
  }
}
