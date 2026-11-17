/**
 * DetailHeader 单测（契约 §8）：
 *  - 面包屑各段渲染、clickable 段类名；
 *  - expose goBack() → onBack 命中。
 */
import { describe, expect, it, vi } from 'vitest';
import DetailHeader from '../../../../components/extensions/ui/DetailHeader.vue';
import { instanceOf, renderWithCapture } from './testUtils';

const BREADCRUMB = [
  { label: '插件', clickable: true },
  { label: 'demo-plugin' },
];

describe('DetailHeader 渲染', () => {
  it('面包屑各段渲染', async () => {
    const { html } = await renderWithCapture(DetailHeader, {
      breadcrumb: BREADCRUMB,
      title: 'demo-plugin',
    });
    expect(html).toContain('插件');
    expect(html).toContain('demo-plugin');
    expect(html).toContain('dh-breadcrumb');
  });

  it('clickable 段（非尾段）为链接按钮，尾段为纯文本', async () => {
    const { html } = await renderWithCapture(DetailHeader, {
      breadcrumb: BREADCRUMB,
      title: 'demo-plugin',
    });
    expect(html).toContain('dh-crumb-link');
    expect(html).toContain('dh-crumb-tail');
  });

  it('尾段即使声明 clickable 也不可点', async () => {
    const { html } = await renderWithCapture(DetailHeader, {
      breadcrumb: [
        { label: '插件', clickable: true },
        { label: '插件市场', clickable: true },
        { label: '当前页', clickable: true },
      ],
      title: '当前页',
    });
    // 前两段可点（2 个 link），尾段不可点
    expect(html.match(/dh-crumb-link/g)?.length).toBe(2);
    expect(html).toContain('dh-crumb-tail');
  });

  it('标题、大图标与 badges 渲染', async () => {
    const { html } = await renderWithCapture(DetailHeader, {
      breadcrumb: BREADCRUMB,
      title: 'demo-plugin',
      badges: [
        { text: '集成', type: 'type', color: 'integration' },
        { text: 'v0.9.1', type: 'ver' },
      ],
    });
    expect(html).toContain('demo-plugin');
    expect(html).toContain('plugin-icon-lg');
    expect(html).toContain('集成');
    expect(html).toContain('v0.9.1');
  });
});

describe('DetailHeader 交互（expose 触发器）', () => {
  it('expose goBack() → onBack 命中', async () => {
    const onBack = vi.fn();
    const { instances } = await renderWithCapture(DetailHeader, {
      breadcrumb: BREADCRUMB,
      title: 'demo-plugin',
      onBack,
    });
    instanceOf(instances, DetailHeader).exposed?.goBack();
    expect(onBack).toHaveBeenCalledTimes(1);
  });
});
