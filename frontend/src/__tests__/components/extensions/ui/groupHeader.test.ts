/**
 * GroupHeader 单测（契约 §8）：title/count 渲染（count 未传则无计数徽章）、
 * actions 插槽内容出现。
 */
import { describe, expect, it } from 'vitest';
import { h } from 'vue';
import GroupHeader from '../../../../components/extensions/ui/GroupHeader.vue';
import AppButton from '../../../../components/ui/AppButton.vue';
import { renderWithCapture } from './testUtils';

describe('GroupHeader 渲染', () => {
  it('title 渲染', async () => {
    const { html } = await renderWithCapture(GroupHeader, {
      title: 'official-market',
    });
    expect(html).toContain('official-market');
    expect(html).toContain('group-header');
  });

  it('count 传入时渲染计数徽章', async () => {
    const { html } = await renderWithCapture(GroupHeader, {
      title: 'official-market',
      count: 7,
    });
    expect(html).toContain('gh-count');
    expect(html).toContain('>7</span>');
  });

  it('count 未传则无计数徽章', async () => {
    const { html } = await renderWithCapture(GroupHeader, {
      title: 'official-market',
    });
    expect(html).not.toContain('gh-count');
  });

  it('actions 插槽内容出现', async () => {
    const { html } = await renderWithCapture(
      GroupHeader,
      { title: 'official-market', count: 3 },
      {
        actions: () => [h(AppButton, { variant: 'ghost', size: 'small' }, () => '更新市场')],
      },
    );
    expect(html).toContain('更新市场');
    expect(html).toContain('gh-actions');
  });
});
