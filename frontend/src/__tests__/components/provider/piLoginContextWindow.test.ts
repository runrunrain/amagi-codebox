import { describe, expect, it } from 'vitest';
import { normalizeContextWindowInput } from '../../../components/provider/piLoginContextWindow';

// F01 回归：number input 的 v-model 被 Vue 隐式转 number（castToNumber），
// 归一化必须兼容 number/string/空值/非法值，不得对 number 调 string 方法。
describe('pi login preset context window input normalization', () => {
  it('accepts Vue-implicit number values from type="number" inputs', () => {
    expect(normalizeContextWindowInput(400000)).toBe(400000);
    expect(normalizeContextWindowInput(0)).toBe(0);
  });

  it('parses trimmed numeric strings', () => {
    expect(normalizeContextWindowInput(' 256000 ')).toBe(256000);
    expect(normalizeContextWindowInput('400000')).toBe(400000);
  });

  it('treats empty, non-numeric, non-positive and nullish input as no-override (0)', () => {
    expect(normalizeContextWindowInput('')).toBe(0);
    expect(normalizeContextWindowInput('abc')).toBe(0);
    expect(normalizeContextWindowInput('-5')).toBe(0);
    expect(normalizeContextWindowInput(null)).toBe(0);
    expect(normalizeContextWindowInput(undefined)).toBe(0);
    expect(normalizeContextWindowInput(Number.NaN)).toBe(0);
  });
});
