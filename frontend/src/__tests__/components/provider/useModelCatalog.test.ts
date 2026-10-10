import { describe, it, expect } from 'vitest';
import { parseModelSpec, buildModelSpec, thinkingLevelsFor, compatibleThinkingLevel } from '../../../components/provider/useModelCatalog';

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
    expect(parseModelSpec('p/:high')).toBeNull();
    expect(parseModelSpec(' p/m')).toBeNull();
    expect(parseModelSpec('p/m\u3000')).toBeNull();
  });

  it('preserves colon model IDs and strips only recognised thinking suffixes', () => {
    expect(parseModelSpec('ollama/qwen:32b')).toEqual({ provider: 'ollama', model: 'qwen:32b', level: '' });
    expect(parseModelSpec('ollama/qwen:32b:max')).toEqual({ provider: 'ollama', model: 'qwen:32b', level: 'max' });
    expect(parseModelSpec('p/m:preview')).toEqual({ provider: 'p', model: 'm:preview', level: '' });
  });

  it('removes unsupported levels only when explicitly switching models', () => {
    const plain = { id: 'plain', reasoning: false };
    const reasoning = { id: 'reasoning', reasoning: true, thinkingLevels: ['low', 'high'] };
    expect(thinkingLevelsFor(plain)).toEqual(['off']);
    expect(compatibleThinkingLevel(plain, 'max')).toBe('');
    expect(compatibleThinkingLevel(reasoning, 'max')).toBe('');
    expect(compatibleThinkingLevel(reasoning, 'high')).toBe('high');
    expect(compatibleThinkingLevel(reasoning, '')).toBe('');
    expect(compatibleThinkingLevel(undefined, 'max')).toBe('max');
  });

  it('builds spec with and without level', () => {
    expect(buildModelSpec('openai', 'gpt-4o', '')).toBe('openai/gpt-4o');
    expect(buildModelSpec('anthropic', 'claude-3-7-sonnet', 'high')).toBe('anthropic/claude-3-7-sonnet:high');
  });
});
