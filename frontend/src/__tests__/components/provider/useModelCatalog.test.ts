import { describe, it, expect } from 'vitest';
import { parseModelSpec, buildModelSpec } from '../../../components/provider/useModelCatalog';

describe('useModelCatalog spec utils', () => {
  it('parses standard provider/model:level spec', () => {
    const parsed = parseModelSpec('openrouter/anthropic/claude-3.5-sonnet:high');
    expect(parsed).toEqual({
      provider: 'openrouter',
      model: 'anthropic/claude-3.5-sonnet',
      level: 'high',
    });
  });

  it('parses spec without thinking level', () => {
    const parsed = parseModelSpec('anthropic/claude-3-7-sonnet');
    expect(parsed).toEqual({
      provider: 'anthropic',
      model: 'claude-3-7-sonnet',
      level: '',
    });
  });

  it('returns null for invalid specs', () => {
    expect(parseModelSpec('')).toBeNull();
    expect(parseModelSpec('invalid')).toBeNull();
    expect(parseModelSpec('/invalid')).toBeNull();
  });

  it('builds spec with and without level', () => {
    expect(buildModelSpec('openai', 'gpt-4o', '')).toBe('openai/gpt-4o');
    expect(buildModelSpec('anthropic', 'claude-3-7-sonnet', 'high')).toBe('anthropic/claude-3-7-sonnet:high');
  });
});
