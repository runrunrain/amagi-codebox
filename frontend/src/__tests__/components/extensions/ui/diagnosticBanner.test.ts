/**
 * DiagnosticBanner 单测（契约 §8 共享组件清单）：
 *  - 两数组皆空 → 整条不渲染；
 *  - 折叠态摘要含「N 个错误，M 个警告」且为传入数组真实长度；
 *  - expose toggleExpand() 后逐条文本出现（同实例重渲，见 testUtils 机制说明）。
 */
import { describe, expect, it } from 'vitest';
import DiagnosticBanner from '../../../../components/extensions/ui/DiagnosticBanner.vue';
import { instanceOf, renderInstanceAgain, renderWithCapture } from './testUtils';

describe('DiagnosticBanner', () => {
  it('两数组皆空时整条不渲染', async () => {
    const { html } = await renderWithCapture(DiagnosticBanner, { errors: [], warnings: [] });
    expect(html).not.toContain('diagnostic-banner');
    expect(html).not.toContain('加载诊断');
  });

  it('仅错误时渲染，折叠态摘要不含条目正文', async () => {
    const { html } = await renderWithCapture(DiagnosticBanner, {
      errors: ['CLI 加载失败'],
      warnings: [],
    });
    expect(html).toContain('diagnostic-banner');
    expect(html).toContain('加载诊断：1 个错误，0 个警告');
    // 折叠态只显示摘要，不逐条列出
    expect(html).not.toContain('CLI 加载失败');
  });

  it('摘要计数为传入数组真实长度（多错误 + 多警告）', async () => {
    const { html } = await renderWithCapture(DiagnosticBanner, {
      errors: ['错误一', '错误二', '错误三'],
      warnings: ['警告一', '警告二'],
    });
    expect(html).toContain('加载诊断：3 个错误，2 个警告');
  });

  it('expose toggleExpand() 后逐条文本出现（同实例重渲）', async () => {
    const { instances } = await renderWithCapture(DiagnosticBanner, {
      errors: ['错误一', '错误二'],
      warnings: ['警告一'],
    });
    const instance = instanceOf(instances, DiagnosticBanner);
    instance.exposed?.toggleExpand();
    const expandedHtml = await renderInstanceAgain(instance);
    expect(expandedHtml).toContain('错误一');
    expect(expandedHtml).toContain('错误二');
    expect(expandedHtml).toContain('警告一');
    expect(expandedHtml).toContain('db-details');
  });
});
