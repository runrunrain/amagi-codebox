import { describe, expect, it } from 'vitest';
import { modelOutputMaximum, normalizeOutputLimitInput } from '../../../components/provider/piLoginOutputLimit';

describe('Pi login output limit', () => {
  it('selects the exact model directory output limit, not the context window', () => {
    const models = [{ id: 'a', maxTokens: 65536, contextWindow: 200000 }, { id: 'b', maxTokens: 32768 }];
    expect(modelOutputMaximum(models, ' a ')).toBe(65536);
    expect(modelOutputMaximum(models, 'b')).toBe(32768);
    expect(modelOutputMaximum(models, 'missing')).toBe(0);
    expect(modelOutputMaximum([{ id: 'a' }], 'a')).toBe(0);
    for (const maxTokens of [-1, 0, NaN, Infinity, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
      expect(modelOutputMaximum([{ id: 'a', maxTokens }], 'a')).toBe(0);
    }
  });
  it('preserves integer token counts and supports resetting to the default', () => {
    for (const input of ['', ' ', '0', 0]) expect(normalizeOutputLimitInput(input)).toBe(0);
    expect(normalizeOutputLimitInput('65536')).toBe(65536);
    expect(normalizeOutputLimitInput(32768)).toBe(32768);
    for (const input of [-1, 1.5, 'broken', NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
      expect(() => normalizeOutputLimitInput(input)).toThrow('输出上限须为非负整数');
    }
  });
});
