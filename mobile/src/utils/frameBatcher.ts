export interface FrameBatcherOptions<T> {
  onFlush: (items: T[]) => void
  flushInterval?: number
  now?: () => number
  schedule?: (callback: () => void) => number
  cancel?: (handle: number) => void
}

export function createFrameBatcher<T>(options: FrameBatcherOptions<T>) {
  const flushInterval = options.flushInterval ?? 50
  const now = options.now ?? (() => Date.now())
  const schedule = options.schedule ?? ((callback) => {
    if (typeof requestAnimationFrame === 'function') {
      return requestAnimationFrame(callback)
    }
    return setTimeout(callback, flushInterval) as unknown as number
  })
  const cancel = options.cancel ?? ((handle) => {
    if (typeof cancelAnimationFrame === 'function') {
      cancelAnimationFrame(handle)
    } else {
      clearTimeout(handle as unknown as ReturnType<typeof setTimeout>)
    }
  })

  let queue: T[] = []
  let scheduledHandle: number | null = null
  let lastFlushAt = 0

  function flush() {
    scheduledHandle = null
    if (queue.length === 0) return
    const items = queue
    queue = []
    lastFlushAt = now()
    options.onFlush(items)
  }

  function scheduleFlush() {
    if (scheduledHandle !== null) return
    const elapsed = now() - lastFlushAt
    if (elapsed >= flushInterval) {
      scheduledHandle = schedule(flush)
      return
    }
    scheduledHandle = setTimeout(flush, flushInterval - elapsed) as unknown as number
  }

  function enqueue(item: T) {
    queue.push(item)
    scheduleFlush()
  }

  function flushNow() {
    if (scheduledHandle !== null) {
      cancel(scheduledHandle)
      scheduledHandle = null
    }
    flush()
  }

  function dispose() {
    if (scheduledHandle !== null) {
      cancel(scheduledHandle)
      scheduledHandle = null
    }
    queue = []
  }

  return { enqueue, flushNow, dispose }
}
