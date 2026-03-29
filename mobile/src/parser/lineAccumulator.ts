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

  function push(chunk: string): string[] {
    const completedLines: string[] = []

    for (let i = 0; i < chunk.length; i++) {
      const char = chunk[i]

      if (char === '\r') {
        if (chunk[i + 1] === '\n') {
          completedLines.push(activeLine)
          activeLine = ''
          i++
        } else {
          activeLine = ''
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
    if (!activeLine) return []
    const lines = [activeLine]
    activeLine = ''
    return lines
  }

  function reset() {
    activeLine = ''
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
