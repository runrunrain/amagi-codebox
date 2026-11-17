/**
 * AppButton 单测：variant→class 映射（契约 §4：outline variant 为 R0 落地项）、
 * size 修饰类、disabled 属性、默认 ghost、插槽内容。
 * 范式沿用 extensions/ui 测试基建：node 环境 + renderToString，仅断言字符串/class。
 */
import { describe, expect, it } from 'vitest';
import { h } from 'vue';
import AppButton from '../../../components/ui/AppButton.vue';
import { renderWithCapture } from '../extensions/ui/testUtils';

describe('AppButton variant class 映射', () => {
  it('默认 variant 为 ghost', async () => {
    const { html } = await renderWithCapture(AppButton, {});
    expect(html).toContain('btn-ghost');
    expect(html).not.toContain('btn-primary');
  });

  it('primary/danger/icon 映射', async () => {
    const primary = await renderWithCapture(AppButton, { variant: 'primary' });
    expect(primary.html).toContain('btn-primary');
    const danger = await renderWithCapture(AppButton, { variant: 'danger' });
    expect(danger.html).toContain('btn-ghost danger');
    const icon = await renderWithCapture(AppButton, { variant: 'icon' });
    expect(icon.html).toContain('icon-btn');
  });

  it('outline 映射 btn-outline（契约 R0 落地项）', async () => {
    const { html } = await renderWithCapture(AppButton, { variant: 'outline' });
    expect(html).toContain('btn-outline');
    expect(html).not.toContain('btn-ghost');
  });

  it('size 修饰类与 disabled 属性', async () => {
    const sm = await renderWithCapture(AppButton, { variant: 'outline', size: 'small' });
    expect(sm.html).toContain('btn-outline');
    expect(sm.html).toContain('btn-sm');
    const lg = await renderWithCapture(AppButton, { size: 'large' });
    expect(lg.html).toContain('btn-lg');
    const disabled = await renderWithCapture(AppButton, { disabled: true });
    expect(disabled.html).toContain('disabled');
  });

  it('插槽内容渲染', async () => {
    const { html } = await renderWithCapture(
      AppButton,
      { variant: 'outline' },
      { default: () => [h('span', null, '浏览插件市场')] },
    );
    expect(html).toContain('浏览插件市场');
  });
});
