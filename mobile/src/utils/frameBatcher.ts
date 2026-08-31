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
  const hasCustomScheduler = options.schedule !== undefined || options.cancel !== undefined

  let queue: T[] = []
  let scheduledType: 'raf' | 'timeout' | 'custom' | null = null
  let scheduledHandle: number | null = null
  let timeoutHandle: ReturnType<typeof setTimeout> | null = null
  let lastFlushAt = 0

  function flush() {
    scheduledType = null
    scheduledHandle = null
    timeoutHandle = null
    if (queue.length === 0) return
    const items = queue
    queue = []
    lastFlushAt = now()
    options.onFlush(items)
  }

  function scheduleFlush() {
    if (scheduledType !== null) return
    const elapsed = now() - lastFlushAt
    if (hasCustomScheduler) {
      scheduledType = 'custom'
      const scheduleFn = options.schedule ?? ((cb) => setTimeout(cb, Math.max(0, flushInterval - elapsed)) as unknown as number)
      scheduledHandle = scheduleFn(flush)
      return
    }

    if (elapsed >= flushInterval && typeof requestAnimationFrame === 'function') {
      scheduledType = 'raf'
      scheduledHandle = requestAnimationFrame(flush)
      return
    }

    scheduledType = 'timeout'
    timeoutHandle = setTimeout(flush, Math.max(0, flushInterval - elapsed))
  }

  function cancelScheduled() {
    if (scheduledType === 'custom') {
      if (options.cancel && scheduledHandle !== null) {
        options.cancel(scheduledHandle)
      } else if (scheduledHandle !== null) {
        clearTimeout(scheduledHandle as unknown as ReturnType<typeof setTimeout>)
      }
    } else if (scheduledType === 'raf' && scheduledHandle !== null) {
      if (typeof cancelAnimationFrame === 'function') {
        cancelAnimationFrame(scheduledHandle)
      }
    } else if (scheduledType === 'timeout' && timeoutHandle !== null) {
      clearTimeout(timeoutHandle)
    }
    scheduledType = null
    scheduledHandle = null
    timeoutHandle = null
  }

  function enqueue(item: T) {
    queue.push(item)
    scheduleFlush()
  }

  function flushNow() {
    cancelScheduled()
    flush()
  }

  function dispose() {
    cancelScheduled()
    queue = []
  }

  return { enqueue, flushNow, dispose }
}
