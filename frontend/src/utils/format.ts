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
 * Mapping: CC -> ClaudeCode, OC -> OpenCode, CX -> Codex.
 * Falls back to the raw tag when unknown.
 */
export function appTypeLabel(tag: string): string {
  const labels: Record<string, string> = {
    CC: 'ClaudeCode',
    OC: 'OpenCode',
    CX: 'Codex',
  };
  return labels[tag] || tag;
}

/**
 * Session tag color.
 * CC -> accent blue, OC -> warning orange, CX -> purple.
 */
export function tagColor(tag: string): string {
  const colors: Record<string, string> = {
    CC: '#007AFF',
    OC: '#FF9500',
    CX: '#AF52DE',
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
