import { computed, ref } from 'vue'
import { GetOutputHistorySnapshot } from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import {
  appendDetailOutputBytes,
  base64ToUint8,
  buildContextSummary,
  buildTranscriptText,
  createDetailOutputState,
  detailOutputStatusClass,
  detailOutputStatusLabel,
  extractDiffBlocks,
  mergeHistorySnapshot,
  type DetailOutputState,
} from '../utils/sessionDetailText'

export function useSessionDetailOutput() {
  const sessionId = ref('')
  const output = ref<DetailOutputState>(createDetailOutputState(''))
  let disposeDataListener: (() => void) | null = null

  const transcriptText = computed(() => buildTranscriptText(output.value))
  const diffBlocks = computed(() => extractDiffBlocks(transcriptText.value))
  const contextSummary = computed(() => buildContextSummary(transcriptText.value))
  const outputStatusLabel = computed(() => detailOutputStatusLabel(output.value))
  const outputStatusClass = computed(() => detailOutputStatusClass(output.value))

  function patch(updater: (state: DetailOutputState) => DetailOutputState) {
    output.value = updater(output.value)
  }

  function appendLiveChunk(seq: number, bytes: Uint8Array) {
    patch((state) => appendDetailOutputBytes(state, seq, bytes))
  }

  function close() {
    disposeDataListener?.()
    disposeDataListener = null
    sessionId.value = ''
    output.value = createDetailOutputState('')
  }

  async function open(nextSessionId: string) {
    close()
    sessionId.value = nextSessionId
    output.value = createDetailOutputState(nextSessionId)
    patch((state) => ({ ...state, historyStatus: 'loading' }))

    disposeDataListener = EventsOn(`pty:data:${nextSessionId}`, (eventData: any) => {
      try {
        let seq: number
        let base64Data: string
        if (eventData && typeof eventData === 'object' && 's' in eventData && 'd' in eventData) {
          seq = eventData.s as number
          base64Data = eventData.d as string
        } else if (typeof eventData === 'string') {
          seq = 0
          base64Data = eventData
        } else {
          return
        }
        appendLiveChunk(seq, base64ToUint8(base64Data))
      } catch {
        patch((state) => ({
          ...state,
          historyStatus: state.historyStatus === 'loading' ? 'error' : state.historyStatus,
          decodeError: true,
        }))
      }
    })

    try {
      const jsonStr = await GetOutputHistorySnapshot(nextSessionId)
      if (sessionId.value !== nextSessionId) return
      patch((state) => mergeHistorySnapshot(state, jsonStr))
    } catch {
      if (sessionId.value !== nextSessionId) return
      patch((state) => ({
        ...state,
        historyStatus: 'error',
        decodeError: true,
      }))
    }
  }

  return {
    sessionId,
    output,
    transcriptText,
    diffBlocks,
    contextSummary,
    outputStatusLabel,
    outputStatusClass,
    open,
    close,
  }
}
