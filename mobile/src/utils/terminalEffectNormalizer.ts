import { stripTuiChars } from './stripTuiChars'

export type TerminalEffectDiagnosticReason = 'ansi' | 'tui' | 'control-characters'

export interface TerminalEffectDiagnostic {
  reason: TerminalEffectDiagnosticReason
  summary: string
  text?: string
  severity?: 'info' | 'warning' | 'error'
}

export interface TerminalEffectNormalizeOptions {
  allowObjectPayload?: boolean
}

export interface TerminalEffectNormalizeResult {
  cleanText: string
  diagnostics: TerminalEffectDiagnostic[]
  transientStatus: string | null
}

const OSC_PATTERN = /\u001B\][^\u0007]*(?:\u0007|\u001B\\)/g
const CSI_PATTERN = /[\u001B\u009B]\[[0-9;?]*[ -/]*[@-~]/g
const ANSI_OR_OSC_PATTERN = /\u001B\[|\u001B\]|\u009B/u
const TUI_DECORATION_PATTERN = /[─│┌┐└┘├┤┬┴┼━┃┏┓┗┛┣┫┳┻╋╔╗╚╝╠╣╦╩╬╭╮╰╯▁▂▃▄▅▆▇█▀▐▌░▒▓]/u
const CONTROL_CHARACTER_PATTERN = /[\u0000-\u0008\u000B\u000C\u000E-\u001A\u001C-\u001F\u007F\u0080-\u009A\u009C-\u009F]/gu
const TRANSIENT_STATUS_PATTERN = /^(?:[⠁-⣿\-\\|/•·*\s]*)?(?:(?:status|progress)\s*:|(?:thinking|writing|reading(?:\s+file)?|processing|loading|running|waiting|working|analyzing|generating|compiling|building|installing)\b)[\s\S]{0,160}$/i

// Known Claude status words for fragment detection (subset reused from
// transcriptNormalizer to avoid circular dependency; keep in sync).
const STATUS_WORDS_FOR_FRAGMENT_CHECK = [
  'thinking', 'writing', 'reading', 'processing',
  'loading', 'running', 'waiting', 'working',
  'analyzing', 'generating', 'compiling', 'building',
  'installing', 'ionizing',
] as const

/**
 * Lightweight check for garbled status-word fragments.
 * Used as a safety net before returning cleanText from terminal-rewrite paths.
 */
function isStatusFragment(candidate: string): boolean {
  const trimmed = candidate.trim()
  if (trimmed.length > 20 || trimmed.length < 4) return false
  if (trimmed.includes(' ')) return false
  const lower = trimmed.toLowerCase()
  // Exact matches are not fragments; they are handled by isTransientStatusLine
  for (const word of STATUS_WORDS_FOR_FRAGMENT_CHECK) {
    if (lower === word) return false
  }
  for (const word of STATUS_WORDS_FOR_FRAGMENT_CHECK) {
    // Quick check: must share significant characters with the word
    let shared = 0
    const wChars = [...word]
    const cChars = [...lower]
    const wCounts = new Map<string, number>()
    for (const ch of wChars) wCounts.set(ch, (wCounts.get(ch) ?? 0) + 1)
    for (const ch of cChars) {
      const c = wCounts.get(ch)
      if (c && c > 0) {
        shared++
        wCounts.set(ch, c - 1)
      }
    }
    const ratio = shared / Math.max(lower.length, word.length)
    if (ratio >= 0.72) return true
  }
  return false
}

function normalizeLineEndings(value: string): string {
  return value.replace(/\r\n/g, '\n')
}

function visibleControlChars(value: string): string {
  return value.replace(CONTROL_CHARACTER_PATTERN, '')
}

function isTransientStatusLine(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return false
  if (TRANSIENT_STATUS_PATTERN.test(trimmed)) return true
  return /^[\-\\|/⠁-⣿]\s+\S/.test(trimmed) && trimmed.length <= 120
}

function stripControlSequences(value: string) {
  let clearLine = false
  let cursorUp = false
  let sawCsi = false

  clearLine = value.includes('\x1b[2K') || value.includes('\u001B[2K')
  cursorUp = value.includes('\x1b[A') || value.includes('\u001B[A')
  const withoutOsc = value.replace(OSC_PATTERN, '')
  const text = withoutOsc.replace(CSI_PATTERN, (sequence) => {
    sawCsi = true
    if (/K$/.test(sequence) && sequence.includes('2')) {
      clearLine = true
    }
    if (/A$/.test(sequence)) {
      cursorUp = true
    }
    return ''
  })

  return { text, clearLine, cursorUp, sawCsi }
}

export class TerminalEffectNormalizer {
  reset() {}

  normalize(chunk: string, _options: TerminalEffectNormalizeOptions = {}): TerminalEffectNormalizeResult {
    const normalized = normalizeLineEndings(chunk)
    const diagnostics: TerminalEffectDiagnostic[] = []
    const sawAnsi = ANSI_OR_OSC_PATTERN.test(normalized)
    const sawTui = TUI_DECORATION_PATTERN.test(normalized)
    const { text, clearLine, cursorUp, sawCsi } = stripControlSequences(normalized)
    const hasCarriageReturn = text.includes('\r')
    const hasTerminalRewrite = hasCarriageReturn || clearLine || cursorUp

    if (sawAnsi || sawCsi) {
      diagnostics.push({
        reason: 'ansi',
        summary: '终端控制序列已按终端语义归一化，未作为正文噪声展示。',
        text: chunk,
        severity: 'info',
      })
    }
    if (sawTui) {
      diagnostics.push({
        reason: 'tui',
        summary: 'TUI 装饰字符已从移动端文本中清洗。',
        text: chunk,
        severity: 'info',
      })
    }

    const cleanWithControls = stripTuiChars(text)
    const controlMatches = cleanWithControls.match(CONTROL_CHARACTER_PATTERN) ?? []
    if (controlMatches.length > 0 && controlMatches.length / Math.max(cleanWithControls.length, 1) > 0.05) {
      return {
        cleanText: '',
        transientStatus: null,
        diagnostics: [{
          reason: 'control-characters',
          summary: '收到大量控制字符，已隔离，避免污染移动端会话视图。',
          text: chunk,
          severity: 'warning',
        }],
      }
    }

    if (hasTerminalRewrite) {
      const fragments = cleanWithControls
        .split(/[\r\n]/)
        .map((line) => visibleControlChars(line).trim())
        .filter(Boolean)
      const lastLine = fragments[fragments.length - 1] ?? ''

      if (!lastLine) {
        return { cleanText: '', diagnostics, transientStatus: null }
      }

      if (isTransientStatusLine(lastLine)) {
        return { cleanText: '', diagnostics, transientStatus: lastLine }
      }

      // Safety net: discard the last line if it's a garbled status fragment
      if (isStatusFragment(lastLine)) {
        return { cleanText: '', diagnostics, transientStatus: lastLine }
      }

      return {
        cleanText: `${lastLine}${normalized.endsWith('\n') ? '\n' : ''}`,
        diagnostics,
        transientStatus: null,
      }
    }

    const cleanText = visibleControlChars(cleanWithControls.replace(/\r/g, '\n'))
    return { cleanText, diagnostics, transientStatus: null }
  }
}

export function createTerminalEffectNormalizer() {
  return new TerminalEffectNormalizer()
}
