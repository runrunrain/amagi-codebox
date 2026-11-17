/**
 * Format Utilities
 * Pure functions for common formatting needs.
 */

/**
 * Get basename from a file path.
 */
export function basename(path: string): string {
  if (!path) return '';
  return path.split(/[/\\]/).pop() || path;
}

/**
 * Application type label for a session/app tag.
 * Truth source: internal/session/types.go AppType const
 *   claudecode / opencode / codex / pi / omp
 * Falls back to the raw tag when unknown.
 */
export function appTypeLabel(tag: string): string {
  const labels: Record<string, string> = {
    claudecode: 'Claude Code',
    opencode: 'OpenCode',
    codex: 'Codex',
    pi: 'Pi',
    omp: 'Oh My Pi',
  };
  return labels[tag] || tag;
}

/**
 * Session tag color (Apple HIG system tones).
 * Truth source: internal/session/types.go AppType const.
 *   claudecode -> accent blue (#007AFF)
 *   opencode   -> warning orange (#FF9500)
 *   codex      -> purple (#AF52DE)
 *   pi         -> system green (#34C759)
 *   omp        -> system teal (#30B0C7)
 * Unknown falls back to tertiary gray.
 */
export function tagColor(tag: string): string {
  const colors: Record<string, string> = {
    claudecode: '#007AFF',
    opencode: '#FF9500',
    codex: '#AF52DE',
    pi: '#34C759',
    omp: '#30B0C7',
  };
  return colors[tag] || '#8E8E93';
}

/**
 * Truncate text with an ellipsis when it exceeds maxLength.
 */
export function truncate(text: string, maxLength: number): string {
  if (!text || text.length <= maxLength) return text;
  return text.slice(0, maxLength - 3) + '...';
}

/**
 * Extract a readable message from an unknown error value.
 */
export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message || String(err);
  if (typeof err === 'string') return err;
  if (err && typeof err === 'object' && 'message' in err) {
    const msg = (err as { message?: unknown }).message;
    if (typeof msg === 'string' && msg) return msg;
  }
  return String(err);
}
