<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import type { RawTerminalSink } from '../../composables/useRawTerminalSink'

const props = defineProps<{
  sink: RawTerminalSink
  visible: boolean
  fontSize?: number
}>()

const terminalHost = ref<HTMLDivElement>()
const xtermAvailable = ref(true)
let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let opened = false
const hiddenFitAttempts = ref(0)

function ensureTerminal() {
  if (!terminalHost.value || opened) return
  try {
    terminal = new Terminal({
      convertEol: true,
      disableStdin: true,
      fontSize: props.fontSize ?? 14,
      fontFamily: '"Cascadia Code", "Fira Code", "JetBrains Mono", monospace',
      theme: { background: '#0d1117', foreground: '#c9d1d9' },
      scrollback: 8000,
    })
    fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
    terminal.open(terminalHost.value)
    if (props.sink.rawText.value) terminal.write(props.sink.rawText.value)
    props.sink.attachXtermWriter((chunk) => terminal?.write(chunk))
    opened = true
  } catch {
    xtermAvailable.value = false
    props.sink.attachXtermWriter(null)
  }
}

function refreshVisibleTerminal() {
  if (!props.visible) {
    hiddenFitAttempts.value += 1
    return
  }
  ensureTerminal()
  requestAnimationFrame(() => {
    if (!props.visible || !terminal) return
    try {
      fitAddon?.fit()
      terminal.refresh(0, Math.max(0, terminal.rows - 1))
      terminal.scrollToBottom()
    } catch {
      xtermAvailable.value = false
    }
  })
}

watch(() => props.visible, async (visible) => {
  if (!visible) return
  await nextTick()
  refreshVisibleTerminal()
}, { immediate: true })

watch(() => props.fontSize, (fontSize) => {
  if (terminal && fontSize) terminal.options.fontSize = fontSize
  if (props.visible) refreshVisibleTerminal()
})

onBeforeUnmount(() => {
  props.sink.attachXtermWriter(null)
  props.sink.dispose()
  terminal?.dispose()
  terminal = null
  fitAddon = null
})
</script>

<template>
  <section class="raw-terminal-panel" aria-label="Raw terminal diagnostics">
    <div class="raw-terminal-toolbar">
      <span>原始终端诊断</span>
      <span class="raw-terminal-counter">flush {{ sink.flushCount.value }} · write {{ sink.writeCount.value }}</span>
    </div>
    <div v-show="xtermAvailable" ref="terminalHost" class="raw-terminal-xterm"></div>
    <pre class="raw-terminal-line-buffer" :class="{ 'raw-terminal-line-buffer--fallback': xtermAvailable }">{{ sink.displayText.value || '等待终端输出...' }}</pre>
  </section>
</template>

<style scoped>
.raw-terminal-panel {
  border-radius: 14px;
  border: 1px solid rgba(139, 148, 158, 0.24);
  background: #0d1117;
  overflow: hidden;
}

.raw-terminal-toolbar {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 10px;
  color: #8b949e;
  font-size: 12px;
  border-bottom: 1px solid rgba(139, 148, 158, 0.18);
}

.raw-terminal-counter {
  color: #6e7681;
}

.raw-terminal-xterm {
  min-height: 260px;
  padding: 8px;
}

.raw-terminal-line-buffer {
  margin: 0;
  min-height: 260px;
  max-height: 62vh;
  overflow: auto;
  padding: 12px;
  color: #c9d1d9;
  white-space: pre-wrap;
  word-break: break-word;
}

.raw-terminal-line-buffer--fallback {
  min-height: 120px;
  max-height: 32vh;
  border-top: 1px solid rgba(139, 148, 158, 0.18);
  background: rgba(1, 4, 9, 0.72);
}
</style>
