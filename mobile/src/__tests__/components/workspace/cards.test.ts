/**
 * __tests__/components/workspace/cards.test.ts — 转化组件交互行为（M2-C）
 * 覆盖：PromptAction 按钮组点按即答/观察者禁用、OptionCard 单选即答、
 * FoldBlock 展开/复制、ProgressCard 控制者停止按钮、Composer 发送/历史复用/
 * 观察者禁用、TimelineView 挂载。
 */
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import PromptActionCard from '../../../components/workspace/PromptActionCard.vue';
import OptionCard from '../../../components/workspace/OptionCard.vue';
import FoldBlock from '../../../components/workspace/FoldBlock.vue';
import ProgressCard from '../../../components/workspace/ProgressCard.vue';
import ComposerBar from '../../../components/workspace/ComposerBar.vue';
import TimelineView from '../../../components/workspace/TimelineView.vue';
import type { FoldItem, OptionItem, ProgressItem, PromptActionItem } from '../../../lib/timeline';

const PROMPT: PromptActionItem = {
  id: 'p1',
  kind: 'prompt-action',
  question: 'Do you want to proceed?',
  options: [
    { key: 'opt-1', label: 'Yes', input: '1' },
    { key: 'opt-2', label: 'No', input: '2' },
  ],
};

const OPTION: OptionItem = {
  id: 'o1',
  kind: 'option',
  title: 'Choose:',
  options: [
    { key: 'opt-1', label: 'Alpha', input: '1' },
    { key: 'opt-2', label: 'Beta', input: '2' },
  ],
};

describe('PromptActionCard', () => {
  it('控制者点按按钮即答（emit answer 携带选项输入）', async () => {
    const wrapper = mount(PromptActionCard, { props: { item: PROMPT, canAnswer: true } });
    const buttons = wrapper.findAll('.prompt-btn');
    expect(buttons).toHaveLength(2);
    await buttons[1].trigger('click');
    expect(wrapper.emitted('answer')).toEqual([['2']]);
  });

  it('观察者：按钮禁用并给出提示', () => {
    const wrapper = mount(PromptActionCard, { props: { item: PROMPT, canAnswer: false } });
    const buttons = wrapper.findAll('.prompt-btn');
    expect(buttons.every((b) => b.attributes('disabled') !== undefined)).toBe(true);
    expect(wrapper.text()).toContain('获取控制权');
  });
});

describe('OptionCard', () => {
  it('单选即答：点按发送该选项输入', async () => {
    const wrapper = mount(OptionCard, { props: { item: OPTION, canAnswer: true } });
    await wrapper.findAll('.option-btn')[0].trigger('click');
    expect(wrapper.emitted('answer')).toEqual([['1']]);
  });

  it('观察者：选项禁用', () => {
    const wrapper = mount(OptionCard, { props: { item: OPTION, canAnswer: false } });
    expect(wrapper.findAll('.option-btn').every((b) => b.attributes('disabled') !== undefined)).toBe(true);
  });
});

describe('FoldBlock', () => {
  const FOLD: FoldItem = {
    id: 'f1',
    kind: 'fold',
    summary: 'build output',
    lineCount: 15,
    fullText: 'line1\nline2\nline3',
  };

  it('默认折叠：摘要行 + 行数；展开呈现等宽全文', async () => {
    const wrapper = mount(FoldBlock, { props: { item: FOLD } });
    expect(wrapper.text()).toContain('build output');
    expect(wrapper.text()).toContain('15 行');
    expect(wrapper.find('.fold-full').exists()).toBe(false);
    await wrapper.find('.fold-toggle').trigger('click');
    expect(wrapper.find('.fold-full').exists()).toBe(true);
    expect(wrapper.find('.fold-full').text()).toContain('line3');
  });

  it('复制按钮调用剪贴板并反馈已复制', async () => {
    const writeText = vi.fn(async () => {});
    Object.assign(navigator, { clipboard: { writeText } });
    const wrapper = mount(FoldBlock, { props: { item: FOLD } });
    await wrapper.find('.fold-copy').trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    expect(writeText).toHaveBeenCalledWith(FOLD.fullText);
    expect(wrapper.text()).toContain('已复制');
  });
});

describe('ProgressCard', () => {
  const PROGRESS: ProgressItem = { id: 'pr1', kind: 'progress', text: 'Building project…' };

  it('控制者可见显式「停止运行」按钮（≥44px 语义由样式保证）', async () => {
    const wrapper = mount(ProgressCard, { props: { item: PROGRESS, canControl: true, stopping: false } });
    const stop = wrapper.find('.progress-stop');
    expect(stop.exists()).toBe(true);
    expect(stop.text()).toBe('停止运行');
    await stop.trigger('click');
    expect(wrapper.emitted('stop')).toHaveLength(1);
  });

  it('观察者无停止按钮，仅文字化进度', () => {
    const wrapper = mount(ProgressCard, { props: { item: PROGRESS, canControl: false, stopping: false } });
    expect(wrapper.find('.progress-stop').exists()).toBe(false);
    expect(wrapper.text()).toContain('Building project');
  });
});

describe('ComposerBar', () => {
  it('发送：emit send；空草稿禁用发送按钮', async () => {
    const wrapper = mount(ComposerBar, {
      props: { draft: 'ls -la', sending: false, stopping: false, canWrite: true, canControl: true, blockReason: null, history: [] },
    });
    await wrapper.find('.composer-send').trigger('click');
    expect(wrapper.emitted('send')).toHaveLength(1);
    // 发送后 draft 由 store 清空 → 按钮禁用（此处模拟）
    await wrapper.setProps({ draft: '' });
    expect(wrapper.find('.composer-send').attributes('disabled')).toBeDefined();
  });

  it('发送态/观察者禁用：sending 中禁用；观察者禁用输入并明示原因', async () => {
    const sendingWrapper = mount(ComposerBar, {
      props: { draft: 'x', sending: true, stopping: false, canWrite: true, canControl: true, blockReason: null, history: [] },
    });
    expect(sendingWrapper.find('.composer-send').attributes('disabled')).toBeDefined();

    const observer = mount(ComposerBar, {
      props: { draft: 'x', sending: false, stopping: false, canWrite: false, canControl: false, blockReason: '桌面端正在控制，你可观察但无法输入', history: [] },
    });
    expect(observer.find('.composer-input').attributes('disabled')).toBeDefined();
    expect(observer.text()).toContain('桌面端正在控制');
    expect(observer.find('.composer-stop').exists()).toBe(false);
  });

  it('历史指令复用：点选回填草稿（emit reuse），不直接发送', async () => {
    const wrapper = mount(ComposerBar, {
      props: { draft: '', sending: false, stopping: false, canWrite: true, canControl: true, blockReason: null, history: ['npm test', 'git status'] },
    });
    await wrapper.find('.composer-history').trigger('click');
    const items = wrapper.findAll('.history-item');
    expect(items).toHaveLength(2);
    await items[1].trigger('click');
    expect(wrapper.emitted('reuse')).toEqual([['git status']]);
    expect(wrapper.emitted('send')).toBeUndefined();
  });

  it('停止运行按钮（控制者）：点击 emit stop；stopping 中禁用', async () => {
    const wrapper = mount(ComposerBar, {
      props: { draft: '', sending: false, stopping: false, canWrite: true, canControl: true, blockReason: null, history: [] },
    });
    const stop = wrapper.find('.composer-stop');
    expect(stop.text()).toBe('停止运行');
    await stop.trigger('click');
    expect(wrapper.emitted('stop')).toHaveLength(1);
    await wrapper.setProps({ stopping: true });
    expect(wrapper.find('.composer-stop').attributes('disabled')).toBeDefined();
  });
});

describe('TimelineView', () => {
  it('空时间线呈现说明；挂载不抛错', () => {
    const wrapper = mount(TimelineView, {
      props: { items: [], outputVersion: 0, canAnswer: false, canControl: false, stopping: false, fillingGapIds: new Set<string>() },
    });
    expect(wrapper.find('.timeline').exists()).toBe(true);
    expect(wrapper.text()).toContain('尚无输出');
  });
});
