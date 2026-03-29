export type DiffLineKind = 'context' | 'add' | 'delete' | 'hunk' | 'file'

export function classifyDiffLine(line: string): DiffLineKind {
  if (line.startsWith('@@')) return 'hunk'
  if (line.startsWith('+++ ') || line.startsWith('--- ')) return 'file'
  if (line.startsWith('+')) return 'add'
  if (line.startsWith('-')) return 'delete'
  return 'context'
}
