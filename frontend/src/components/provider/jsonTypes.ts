export type JsonRecord = Record<string, unknown>

export function isJsonRecord(v: unknown): v is JsonRecord {
  return Boolean(v) && typeof v === 'object' && !Array.isArray(v)
}

export function asJsonRecord(v: unknown): JsonRecord {
  return isJsonRecord(v) ? v : {}
}
