/**
 * __tests__/components/lobby/StatusBar.test.ts — StatusBar 压缩视图（Web 平面）行为
 * suppressAutoExpand：异常层不强制展开明细（胶囊色调仍提示），手动展开不受限。
 */
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import StatusBar from '../../../components/lobby/StatusBar.vue';
import type { StatusLayer } from '../../../components/lobby/StatusBar.vue';

const abnormalLayers: StatusLayer[] = [
  { key: 'connection', label: '连接', text: '已连接', tone: 'ok' },
  { key: 'control', label: '控制', text: '桌面端控制中', tone: 'warning', detail: '你可观察，无法输入' },
  { key: 'history', label: '历史', text: '存在缺口', tone: 'danger', detail: '不可恢复的缺口以标记原样保留' },
];

describe('StatusBar.vue — suppressAutoExpand', () => {
  it('默认行为：异常层强制展开明细列表（既有语义零回归）', () => {
    const wrapper = mount(StatusBar, { props: { layers: abnormalLayers } });
    expect(wrapper.find('.layer-details').exists()).toBe(true);
  });

  it('suppressAutoExpand：异常层不展开明细（面积还给会话内容），异常胶囊仍展示', () => {
    const wrapper = mount(StatusBar, { props: { layers: abnormalLayers, suppressAutoExpand: true } });
    expect(wrapper.find('.layer-details').exists()).toBe(false);
    const chips = wrapper.findAll('.chip');
    expect(chips.length).toBe(abnormalLayers.length);
    expect(chips.some((c) => c.classes().includes('chip--warning'))).toBe(true);
    expect(chips.some((c) => c.classes().includes('chip--danger'))).toBe(true);
  });

  it('suppressAutoExpand 下用户仍可手动展开/收起明细', async () => {
    const wrapper = mount(StatusBar, { props: { layers: abnormalLayers, suppressAutoExpand: true } });
    await wrapper.find('.chip-toggle').trigger('click');
    expect(wrapper.find('.layer-details').exists()).toBe(true);
    await wrapper.find('.chip-toggle').trigger('click');
    expect(wrapper.find('.layer-details').exists()).toBe(false);
  });
});
