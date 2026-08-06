import * as fs from 'node:fs'
import * as path from 'node:path'

export interface DistributionSummary {
  count: number
  min: number
  p50: number
  p95: number
  max: number
  mean: number
  stddev: number
}

/** R-7 linear interpolation quantile (same input always produces the same result). */
export function quantile(values: readonly number[], q: number): number {
  if (values.length === 0) return Number.NaN
  const sorted = [...values].sort((a, b) => a - b)
  const index = (sorted.length - 1) * q
  const lo = Math.floor(index)
  const hi = Math.ceil(index)
  if (lo === hi) return sorted[lo]
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (index - lo)
}

function rounded(value: number): number {
  return Number(value.toFixed(3))
}

export function summarize(values: readonly number[]): DistributionSummary {
  if (values.length === 0) {
    return { count: 0, min: Number.NaN, p50: Number.NaN, p95: Number.NaN, max: Number.NaN, mean: Number.NaN, stddev: Number.NaN }
  }
  const mean = values.reduce((sum, value) => sum + value, 0) / values.length
  const variance = values.reduce((sum, value) => sum + (value - mean) ** 2, 0) / values.length
  return {
    count: values.length,
    min: rounded(Math.min(...values)),
    p50: rounded(quantile(values, 0.5)),
    p95: rounded(quantile(values, 0.95)),
    max: rounded(Math.max(...values)),
    mean: rounded(mean),
    stddev: rounded(Math.sqrt(variance)),
  }
}

export function ensureDir(dir: string): void {
  fs.mkdirSync(dir, { recursive: true })
}

export function writeJson(file: string, value: unknown): void {
  ensureDir(path.dirname(file))
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`, 'utf8')
}

function csvCell(value: unknown): string {
  if (value === null || value === undefined) return ''
  const text = typeof value === 'object' ? JSON.stringify(value) : String(value)
  return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

export function writeCsv(file: string, rows: readonly Record<string, unknown>[]): void {
  ensureDir(path.dirname(file))
  const columns = [...new Set(rows.flatMap((row) => Object.keys(row)))]
  const lines = [columns.map(csvCell).join(',')]
  for (const row of rows) lines.push(columns.map((column) => csvCell(row[column])).join(','))
  fs.writeFileSync(file, `${lines.join('\n')}\n`, 'utf8')
}
