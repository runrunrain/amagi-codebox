import { stripTuiChars } from './stripTuiChars'
import { createTerminalEffectNormalizer } from './terminalEffectNormalizer'

export type DiagnosticReason =
  | 'ansi'
  | 'tui'
  | 'unsupported-pattern'
  | 'fallback'
  | 'parser-error'
  | 'classifier-overflow'
  | 'schema-invalid'
  | 'unknown-frame'
  | 'decode-error'
  | 'control-characters'
  | 'object-payload'
  | 'invalid-part'
  | 'unrecoverable-raw-terminal'
  | 'orphan-delta'
  | 'history-truncated'

export interface TranscriptDiagnosticInput {
  reason: DiagnosticReason
  summary: string
  text?: string
  seq?: number
  severity?: 'info' | 'warning' | 'error'
}

export interface TranscriptDiagnosticRecord extends Required<Omit<TranscriptDiagnosticInput, 'text' | 'seq'>> {
  id: string
  preview: string
  redacted: boolean
  seq?: number
  createdAt: string
}

export interface NormalizedTranscriptChunk {
  cleanText: string
  diagnostics: TranscriptDiagnosticInput[]
  transientStatus?: string | null
}

const MAX_DIAGNOSTIC_PREVIEW_CHARS = 800
const SUSPICIOUS_OBJECT_PATTERN = /^\s*(?:\{[\s\S]*\}|\[[\s\S]*\]|\[object\s+(?:Object|Uint8Array|ArrayBuffer|Blob|File)\])\s*$/i
const CONTROL_CHARACTER_PATTERN = /[\u0000-\u0008\u000B\u000C\u000E-\u001A\u001C-\u001F\u007F\u0080-\u009A\u009C-\u009F]/gu
const TUI_DECORATION_PATTERN = /[─│┌┐└┘├┤┬┴┼━┃┏┓┗┛┣┫┳┻╋╔╗╚╝╠╣╦╩╬╭╮╰╯▁▂▃▄▅▆▇█▀▐▌░▒▓]/u
const ANSI_OR_OSC_PATTERN = /\u001B\[|\u001B\]|\u001B[()#]|\u009B/u
const SPINNER_ONLY_PATTERN = /^[\s\-\\|/⠁-⣿•·*]+$/u
const TRANSIENT_STATUS_TEXT_PATTERN = /^(?:[\-\\|/⠁-⣿•·*]\s*)?(?:thinking|writing|reading(?:\s+file)?|processing|loading|running|waiting|working|analyzing|generating|compiling|building|installing|ionizing)(?:[\s.:…-]*\d*)?(?:\s+with\s+(?:low|medium|high)\s+effort)?$/i
const TUI_HINT_PATTERN = /(?:press\s+(?:enter|esc)|[↑↓←→]\s*(?:navigate|select)|(?:esc|ctrl\+[a-z]|tab)\s*(?:to|:)|^\s*[❯›>]\s*\S)/i
const CLAUDE_STATUS_WORD_PATTERN = /(?:thinking|[a-z]{0,6}hinking|tnking|tinking)\s+with\s+(?:low|medium|high)\s+effort/i
const CLAUDE_STATUS_LINE_PATTERN = /^(?:[↓↑]\s*)?(?:\d+\s+tokens?\s*[·.]\s*)?(?:\(?\d+s\s*[·.]\s*)?(?:thinking|[a-z]{0,6}hinking|tnking|tinking)\s+with\s+(?:low|medium|high)\s+effort\)?$/i
const CLAUDE_TUI_HINT_LINE_PATTERN = /\b(?:bypass permissions|shift\+tab|esc to interrupt|for agents)\b/i
const CLAUDE_STARTUP_TIP_PATTERN = /^Tip:\s*Running multiple Claude sessions\?/i
const CLAUDE_NOISE_SHORT_LINE_PATTERN = /^(?:Waddling\.{3}|\*?Cooked for \d+s|[>›❯]+)$/i
const READABLE_MARKDOWN_PATTERN = /^(?:#{1,6}\s+|[-*+]\s+|\d+\.\s+|>\s+|```|\|.+\|)|\[[^\]]+\]\([^\)]+\)|\*\*[^*]+\*\*/m
const SECRET_PATTERNS: Array<[RegExp, string]> = [
  [/\b(sk-[A-Za-z0-9_-]{12,})\b/g, 'sk-[REDACTED]'],
  [/\b((?:api[_-]?key|token|secret|password)\s*[:=]\s*)([^\s&"']+)/gi, '$1[REDACTED]'],
  [/\b([A-Za-z0-9._%+-]+)@([A-Za-z0-9.-]+\.[A-Za-z]{2,})\b/g, '[REDACTED_EMAIL]'],
]
const terminalEffectNormalizer = createTerminalEffectNormalizer()

function normalizeLineEndings(value: string): string {
  return value.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
}

export function cleanPtyText(value: string): string {
  return stripTuiChars(normalizeLineEndings(value))
    .replace(CONTROL_CHARACTER_PATTERN, '')
}

export function redactDiagnosticText(value: string): { text: string; redacted: boolean } {
  let text = cleanPtyText(value)
  let redacted = false
  for (const [pattern, replacement] of SECRET_PATTERNS) {
    const next = text.replace(pattern, replacement)
    if (next !== text) {
      redacted = true
      text = next
    }
  }
  if (text.length > MAX_DIAGNOSTIC_PREVIEW_CHARS) {
    text = `${text.slice(0, MAX_DIAGNOSTIC_PREVIEW_CHARS)}…`
  }
  return { text, redacted }
}

export function createDiagnosticRecord(input: TranscriptDiagnosticInput, id: string, createdAt: string): TranscriptDiagnosticRecord {
  const { text: preview, redacted } = redactDiagnosticText(input.text ?? '')
  return {
    id,
    reason: input.reason,
    summary: input.summary,
    preview,
    redacted,
    seq: input.seq,
    severity: input.severity ?? 'warning',
    createdAt,
  }
}

function isJsonObjectLike(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return false
  if (/^\[object\s+/i.test(trimmed)) return true
  if (!((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']')))) {
    return false
  }
  try {
    const parsed = JSON.parse(trimmed)
    return parsed !== null && typeof parsed === 'object'
  } catch {
    return false
  }
}

function isFencedCodeOrMarkdownJson(value: string): boolean {
  const trimmed = value.trim()
  return /^```(?:json|jsonc|javascript|typescript|ts|js|\w+)?\s*[\r\n][\s\S]*[\r\n]```$/i.test(trimmed)
}

function normalizeNoiseCandidate(line: string): string {
  return cleanPtyText(line)
    .replace(/\s+/gu, ' ')
    .replace(/^[^\p{L}\p{N}(>›❯↓↑←→*]+/u, '')
    .trim()
}

export function isTerminalTranscriptNoiseLine(line: string): boolean {
  const candidate = normalizeNoiseCandidate(line)
  if (!candidate) return false
  if (candidate.length > 220) return false
  if (CLAUDE_STATUS_LINE_PATTERN.test(candidate)) return true
  if (CLAUDE_STATUS_WORD_PATTERN.test(candidate) && /^(?:[↓↑]\s*)?(?:\d+\s+tokens?\s*[·.]\s*)?\(?[\w\s·.()-]+\)?$/i.test(candidate)) return true
  if (CLAUDE_TUI_HINT_LINE_PATTERN.test(candidate)) return true
  if (CLAUDE_STARTUP_TIP_PATTERN.test(candidate)) return true
  if (CLAUDE_NOISE_SHORT_LINE_PATTERN.test(candidate)) return true
  return false
}

function filterTerminalTranscriptNoise(value: string): { text: string; removed: string[] } {
  if (!value) return { text: value, removed: [] }

  const removed: string[] = []
  const kept = value.split('\n').filter((line) => {
    if (isTerminalTranscriptNoiseLine(line)) {
      removed.push(line)
      return false
    }
    return true
  })

  if (removed.length === 0) return { text: value, removed }
  return {
    text: kept.join('\n').replace(/\n{3,}/g, '\n\n'),
    removed,
  }
}

export function isReadableLegacyText(value: string): boolean {
  const trimmed = cleanPtyText(value).trim()
  if (!trimmed) return false
  if (ANSI_OR_OSC_PATTERN.test(value) || CONTROL_CHARACTER_PATTERN.test(value)) return false
  if (TUI_DECORATION_PATTERN.test(value) || TUI_DECORATION_PATTERN.test(trimmed)) return false
  if (isJsonObjectLike(trimmed) || SUSPICIOUS_OBJECT_PATTERN.test(trimmed)) return false
  if (SPINNER_ONLY_PATTERN.test(trimmed)) return false
  if (TRANSIENT_STATUS_TEXT_PATTERN.test(trimmed)) return false
  if (isTerminalTranscriptNoiseLine(trimmed)) return false
  if (TUI_HINT_PATTERN.test(trimmed)) return false
  if (/^[\d\s.,:%/\\|+-]+$/.test(trimmed)) return false
  if (/^[A-Za-z][\w -]{0,40}\.\.\.\d*$/.test(trimmed)) return false
  if (READABLE_MARKDOWN_PATTERN.test(trimmed)) return true
  if (/(?:error|warning|success|done|created|updated|deleted|changed|files?|lines?|passed|failed|installed|running|read|write|edit|diff|git|npm|pnpm|yarn|build|test)\b/i.test(trimmed)) return true
  return /\p{L}/u.test(trimmed) && trimmed.length >= 3
}

export function normalizeTranscriptChunk(chunk: string): NormalizedTranscriptChunk {
  const effectResult = terminalEffectNormalizer.normalize(chunk)
  const normalized = normalizeLineEndings(effectResult.cleanText)
  const diagnostics: TranscriptDiagnosticInput[] = []
  const initialCleanText = cleanPtyText(normalized)
  const filtered = filterTerminalTranscriptNoise(initialCleanText)
  const cleanText = filtered.text
  const controlMatches = normalized.match(CONTROL_CHARACTER_PATTERN) ?? []

  diagnostics.push(...effectResult.diagnostics)
  if (filtered.removed.length > 0) {
    diagnostics.push({
      reason: 'tui',
      summary: 'Claude Code 终端状态刷新与快捷键提示已隐藏，避免污染移动端会话正文。',
      text: filtered.removed.join('\n'),
      severity: 'info',
    })
  }

  if (!isFencedCodeOrMarkdownJson(normalized) && (isJsonObjectLike(normalized) || SUSPICIOUS_OBJECT_PATTERN.test(normalized))) {
    diagnostics.push({
      reason: 'object-payload',
      summary: '收到疑似对象或 JSON 负载，已隔离，未作为会话正文展示。',
      text: normalized,
      severity: 'warning',
    })
    return { cleanText: '', diagnostics, transientStatus: effectResult.transientStatus }
  }

  if (controlMatches.length > 0 && controlMatches.length / Math.max(normalized.length, 1) > 0.05) {
    diagnostics.push({
      reason: 'control-characters',
      summary: '收到大量控制字符，已隔离，避免污染移动端会话视图。',
      text: normalized,
      severity: 'warning',
    })
    return { cleanText: '', diagnostics, transientStatus: effectResult.transientStatus }
  }

  if (ANSI_OR_OSC_PATTERN.test(normalized)) {
    diagnostics.push({
      reason: 'ansi',
      summary: '终端控制序列已从移动端原始文本中清洗。',
      text: normalized,
      severity: 'info',
    })
  }
  if (TUI_DECORATION_PATTERN.test(normalized)) {
    diagnostics.push({
      reason: 'tui',
      summary: 'TUI 装饰字符已从移动端原始文本中清洗。',
      text: normalized,
      severity: 'info',
    })
  }

  return { cleanText, diagnostics, transientStatus: effectResult.transientStatus }
}

export function resetTranscriptNormalizerState() {
  terminalEffectNormalizer.reset()
}
