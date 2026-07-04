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
 *   claudecode / opencode / codex (+ amagicode legacy)
 * Falls back to the raw tag when unknown.
 */
export function appTypeLabel(tag: string): string {
  const labels: Record<string, string> = {
    claudecode: 'Claude Code',
    opencode: 'OpenCode',
    codex: 'Codex',
    amagicode: 'AmagiCode',
  };
  return labels[tag] || tag;
}

/**
 * Session tag color (Apple HIG system tones).
 * Truth source: internal/session/types.go AppType const.
 *   claudecode -> accent blue (#007AFF)
 *   opencode   -> warning orange (#FF9500)
 *   codex      -> purple (#AF52DE)
 *   amagicode  -> tertiary gray (legacy)
 * Unknown falls back to tertiary gray.
 */
export function tagColor(tag: string): string {
  const colors: Record<string, string> = {
    claudecode: '#007AFF',
    opencode: '#FF9500',
    codex: '#AF52DE',
    amagicode: '#8E8E93',
  };
  return colors[tag] || '#8E8E93';
}

/**
 * Plugin badge color by plugin sub-item type.
 * Returns a CSS value (token reference or hex) suitable for badge backgrounds.
 */
export function pluginBadgeColor(type: string): string {
  const colors: Record<string, string> = {
    integration: 'var(--accent)',
    hybrid: 'var(--purple)',
    skill: '#5AC8FA',
    hook: 'var(--warning)',
    command: 'var(--success)',
    agent: '#FF2D55',
    mcp: '#5AC8FA',
    unknown: 'var(--tertiary)',
  };
  return colors[type] || colors.unknown;
}

/**
 * Format badge color.
 * A -> Anthropic format (purple), O -> OpenAI format (green).
 */
export function formatBadgeColor(fmt: string): string {
  const colors: Record<string, string> = {
    A: 'var(--purple)',
    O: 'var(--success)',
  };
  return colors[fmt] || 'var(--tertiary)';
}

/**
 * Mask a sensitive value (API keys, tokens). Shows prefix when not revealed.
 */
export function maskValue(value: string, visible = false): string {
  if (!value) return '';
  if (visible) return value;
  if (value.length <= 8) return '•'.repeat(value.length);
  return value.slice(0, 8) + '•'.repeat(Math.min(value.length - 8, 12));
}

/**
 * Format a byte count into a human readable string.
 */
export function formatFileSize(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

/**
 * Format a number using locale grouping.
 */
export function formatNumber(num: number): string {
  return num.toLocaleString();
}

/**
 * Truncate text with an ellipsis when it exceeds maxLength.
 */
export function truncate(text: string, maxLength: number): string {
  if (!text || text.length <= maxLength) return text;
  return text.slice(0, maxLength - 3) + '...';
}
