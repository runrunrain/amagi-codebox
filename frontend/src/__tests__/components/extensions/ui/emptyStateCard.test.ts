/**
 * EmptyStateCard 单测（契约 §8）：插槽按钮文案渲染（primary/secondary 各放一个
 * AppButton，与 R1 面板实际用法一致）。
 */
import { describe, expect, it } from 'vitest';
import { h } from 'vue';
import EmptyStateCard from '../../../../components/extensions/ui/EmptyStateCard.vue';
import AppButton from '../../../../components/ui/AppButton.vue';
import { renderWithCapture } from './testUtils';

describe('EmptyStateCard 渲染', () => {
  it('标题与副文案渲染', async () => {
    const { html } = await renderWithCapture(EmptyStateCard, {
      title: '暂无已安装插件',
      description: '从插件市场安装 Claude 插件后将显示在此处',
    });
    expect(html).toContain('暂无已安装插件');
    expect(html).toContain('从插件市场安装 Claude 插件后将显示在此处');
    expect(html).toContain('empty-state-card');
  });

  it('primary/secondary 插槽按钮文案渲染', async () => {
    const { html } = await renderWithCapture(
      EmptyStateCard,
      { title: '暂无已安装插件' },
      {
        primary: () => [h(AppButton, { variant: 'primary' }, () => '浏览插件市场')],
        secondary: () => [h(AppButton, { variant: 'ghost' }, () => '添加市场')],
      },
    );
    expect(html).toContain('浏览插件市场');
    expect(html).toContain('添加市场');
    expect(html).toContain('esc-actions');
  });

  it('仅 primary 插槽时不渲染 secondary 槽位', async () => {
    const { html } = await renderWithCapture(
      EmptyStateCard,
      { title: '暂无 OpenCode 全局插件' },
      { primary: () => [h(AppButton, { variant: 'primary' }, () => '安装插件')] },
    );
    expect(html).toContain('安装插件');
    expect(html).not.toContain('esc-secondary');
  });

  it('description 未提供时不渲染副文案元素', async () => {
    const { html } = await renderWithCapture(EmptyStateCard, { title: '空' });
    expect(html).not.toContain('esc-desc');
  });
});
