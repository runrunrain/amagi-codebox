/**
 * ListToolbar 单测（契约 §8）：标题 + 计数徽章真实数字渲染、loading=true 时
 * 刷新态类名/禁用标记（is-loading + aria-busy）、actions 插槽内容出现。
 */
import { describe, expect, it } from 'vitest';
import { h } from 'vue';
import ListToolbar from '../../../../components/extensions/ui/ListToolbar.vue';
import AppButton from '../../../../components/ui/AppButton.vue';
import { renderWithCapture } from './testUtils';

describe('ListToolbar 渲染', () => {
  it('标题与计数徽章真实数字渲染', async () => {
    const { html } = await renderWithCapture(ListToolbar, {
      title: '已安装',
      count: 12,
    });
    expect(html).toContain('已安装');
    expect(html).toContain('lt-count');
    expect(html).toContain('>12</span>');
  });

  it('loading=true 时带 is-loading 类名与 aria-busy 标记', async () => {
    const { html } = await renderWithCapture(ListToolbar, {
      title: '已安装',
      count: 0,
      loading: true,
    });
    expect(html).toContain('list-toolbar is-loading');
    expect(html).toContain('aria-busy="true"');
  });

  it('loading=false（默认）时无 is-loading 类名', async () => {
    const { html } = await renderWithCapture(ListToolbar, {
      title: '已安装',
      count: 0,
    });
    expect(html).not.toContain('is-loading');
    expect(html).not.toContain('aria-busy');
  });

  it('actions 插槽内容出现', async () => {
    const { html } = await renderWithCapture(
      ListToolbar,
      { title: '已安装', count: 3 },
      {
        actions: () => [
          h(AppButton, { variant: 'ghost', size: 'small' }, () => '刷新'),
          h(AppButton, { variant: 'ghost', size: 'small' }, () => '浏览插件市场'),
        ],
      },
    );
    expect(html).toContain('刷新');
    expect(html).toContain('浏览插件市场');
    expect(html).toContain('lt-actions');
  });
});
