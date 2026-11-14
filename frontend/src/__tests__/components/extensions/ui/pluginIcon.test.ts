/**
 * PluginIcon 单测（契约 §8）：
 *  - 纯函数 pluginIconStyle：同名确定性、不同名分布不同色相、空名/空白名兜底「#」；
 *  - 组件渲染断言：letter 出现在输出。
 */
import { describe, expect, it } from 'vitest';
import PluginIcon, { pluginIconStyle } from '../../../../components/extensions/ui/PluginIcon.vue';
import { renderWithCapture } from './testUtils';

describe('pluginIconStyle（确定性算法纯函数）', () => {
  it('同名恒同 letter 同 color（确定性）', () => {
    const first = pluginIconStyle('super-plugin');
    const second = pluginIconStyle('super-plugin');
    expect(first).toEqual(second);
    expect(first.letter).toBe('S');
  });

  it('letter 取 trim 后首字符大写', () => {
    expect(pluginIconStyle('  opencode-tools').letter).toBe('O');
    expect(pluginIconStyle('混合名称').letter).toBe('混');
  });

  it('空名/空白名兜底「#」', () => {
    expect(pluginIconStyle('').letter).toBe('#');
    expect(pluginIconStyle('   ').letter).toBe('#');
  });

  it('color 恒为五色 token 色板之一', () => {
    const palette = [
      'var(--accent)',
      'var(--success)',
      'var(--warning)',
      'var(--purple)',
      'var(--danger)',
    ];
    const names = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'long-plugin-name', 'x1'];
    for (const name of names) {
      expect(palette).toContain(pluginIconStyle(name).color);
    }
  });

  it('不同名分布不同色相（样本集内出现 ≥2 种颜色）', () => {
    const names = [
      'alpha', 'beta', 'gamma', 'delta', 'epsilon',
      'zeta', 'eta', 'theta', 'iota', 'kappa',
      'lambda', 'mu', 'nu', 'xi', 'omicron',
    ];
    const colors = new Set(names.map((name) => pluginIconStyle(name).color));
    expect(colors.size).toBeGreaterThanOrEqual(2);
  });

  it('哈希符合契约公式 Σ charCodeAt(i)*31^i (mod 997) 的色板下标', () => {
    // 独立复算（直接求幂 + 取模），验证实现未偏离契约公式
    const name = 'contract-check';
    let expected = 0;
    for (let i = 0; i < name.length; i += 1) {
      expected = (expected + name.charCodeAt(i) * 31 ** i) % 997;
    }
    const palette = [
      'var(--accent)',
      'var(--success)',
      'var(--warning)',
      'var(--purple)',
      'var(--danger)',
    ];
    expect(pluginIconStyle(name).color).toBe(palette[expected % 5]);
  });
});

describe('PluginIcon 组件渲染', () => {
  it('letter 出现在输出（sm/md/lg 尺寸类名）', async () => {
    const { html } = await renderWithCapture(PluginIcon, { name: 'demo-plugin', size: 'lg' });
    expect(html).toContain('plugin-icon');
    expect(html).toContain('plugin-icon-lg');
    expect(html).toContain('>D</span>');
  });

  it('默认尺寸为 md', async () => {
    const { html } = await renderWithCapture(PluginIcon, { name: 'any' });
    expect(html).toContain('plugin-icon-md');
  });
});
