import { stripTuiChars } from './stripTuiChars'

export type DiagnosticReason =
  | 'ansi'
  | 'tui'
  | 'unsupported-pattern'
  | 'classifier-overflow'
  | 'schema-invalid'
  | 'unknown-frame'
  | 'decode-error'
  | 'control-characters'
  | 'object-payload'
  | 'invalid-part'

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
}

const MAX_DIAGNOSTIC_PREVIEW_CHARS = 800
const SUSPICIOUS_OBJECT_PATTERN = /^\s*(?:\{[\s\S]*\}|\[[\s\S]*\]|\[object\s+(?:Object|Uint8Array|ArrayBuffer|Blob|File)\])\s*$/i
const CONTROL_CHARACTER_PATTERN = /[\u0000-\u0008\u000B\u000C\u000E-\u001A\u001C-\u001F\u007F\u0080-\u009A\u009C-\u009F]/gu
const TUI_DECORATION_PATTERN = /[─│┌┐└┘├┤┬┴┼━┃┏┓┗┛┣┫┳┻╋╔╗╚╝╠╣╦╩╬╭╮╰╯▁▂▃▄▅▆▇█▀▐▌░▒▓]/u
const ANSI_OR_OSC_PATTERN = /\u001B\[|\u001B\]|\u001B[()#]|\u009B/u
const SECRET_PATTERNS: Array<[RegExp, string]> = [
  [/\b(sk-[A-Za-z0-9_-]{12,})\b/g, 'sk-[REDACTED]'],
  [/\b((?:api[_-]?key|token|secret|password)\s*[:=]\s*)([^\s&"']+)/gi, '$1[REDACTED]'],
  [/\b([A-Za-z0-9._%+-]+)@([A-Za-z0-9.-]+\.[A-Za-z]{2,})\b/g, '[REDACTED_EMAIL]'],
]

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

export function normalizeTranscriptChunk(chunk: string): NormalizedTranscriptChunk {
  const normalized = normalizeLineEndings(chunk)
  const diagnostics: TranscriptDiagnosticInput[] = []
  const cleanText = cleanPtyText(normalized)
  const controlMatches = normalized.match(CONTROL_CHARACTER_PATTERN) ?? []

  if (isJsonObjectLike(normalized) || SUSPICIOUS_OBJECT_PATTERN.test(normalized)) {
    diagnostics.push({
      reason: 'object-payload',
      summary: '收到疑似对象或 JSON 负载，已隔离，未作为会话正文展示。',
      text: normalized,
      severity: 'warning',
    })
    return { cleanText: '', diagnostics }
  }

  if (controlMatches.length > 0 && controlMatches.length / Math.max(normalized.length, 1) > 0.05) {
    diagnostics.push({
      reason: 'control-characters',
      summary: '收到大量控制字符，已隔离，避免污染移动端会话视图。',
      text: normalized,
      severity: 'warning',
    })
    return { cleanText: '', diagnostics }
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

  return { cleanText, diagnostics }
}
