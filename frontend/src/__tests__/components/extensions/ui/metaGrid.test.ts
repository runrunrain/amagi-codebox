/**
 * MetaGrid 单测（契约 §8）：label/value 成对渲染、mono class。
 */
import { describe, expect, it } from 'vitest';
import MetaGrid from '../../../../components/extensions/ui/MetaGrid.vue';
import { renderWithCapture } from './testUtils';

describe('MetaGrid 渲染', () => {
  it('label/value 成对渲染', async () => {
    const { html } = await renderWithCapture(MetaGrid, {
      items: [
        { label: '来源市场', value: 'official' },
        { label: '版本', value: 'v1.0.0' },
        { label: '提交', value: 'a1b2c3d4', mono: true },
      ],
    });
    expect(html).toContain('来源市场');
    expect(html).toContain('official');
    expect(html).toContain('版本');
    expect(html).toContain('v1.0.0');
    expect(html).toContain('提交');
    expect(html).toContain('a1b2c3d4');
  });

  it('mono 项带 mono 类名，普通项不带', async () => {
    const { html } = await renderWithCapture(MetaGrid, {
      items: [
        { label: '作用域', value: 'user' },
        { label: 'Plugin ID', value: 'org.example.demo', mono: true },
      ],
    });
    expect(html).toContain('meta-value mono');
    // 存在未加 mono 的普通值类名
    expect(html).toContain('class="meta-value"');
  });

  it('空 items 渲染空网格', async () => {
    const { html } = await renderWithCapture(MetaGrid, { items: [] });
    expect(html).toContain('meta-grid');
    expect(html).not.toContain('meta-cell');
  });
});
