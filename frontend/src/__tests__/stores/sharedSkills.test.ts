/**
 * stores.sharedSkills 单测（面板门控数据源 + 导入动作）：
 *  - load：成功替换列表并置 loaded；失败写 error 不 rethrow；
 *  - setEnabled/remove：响应列表整体替换；
 *  - importFromZip/importFromFolder：取消（pick 空串）→ null 且不调导入；
 *    成功 → skills 替换为信封 views、返回信封；失败 → rethrow 且列表不动；
 *    importing 标记在完成后复位。
 * 范式同 ompPlugin store 测试：在 api 模块边界打桩。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const apiMocks = vi.hoisted(() => ({
  listSharedSkills: vi.fn(),
  setSharedSkillEnabled: vi.fn(),
  deleteSharedSkill: vi.fn(),
  importSharedSkillsZip: vi.fn(),
  importSharedSkillsDir: vi.fn(),
  pickSharedSkillsZip: vi.fn(),
  pickSharedSkillsDir: vi.fn(),
}));

vi.mock('../../api/sharedSkills', () => ({
  listSharedSkills: apiMocks.listSharedSkills,
  setSharedSkillEnabled: apiMocks.setSharedSkillEnabled,
  deleteSharedSkill: apiMocks.deleteSharedSkill,
  openSharedSkillFolder: vi.fn(),
  openSharedSkillsRoot: vi.fn(),
  importSharedSkillsZip: apiMocks.importSharedSkillsZip,
  importSharedSkillsDir: apiMocks.importSharedSkillsDir,
  pickSharedSkillsZip: apiMocks.pickSharedSkillsZip,
  pickSharedSkillsDir: apiMocks.pickSharedSkillsDir,
}));

import { useSharedSkillsStore } from '../../stores/sharedSkills';
import type { SharedSkillView, SharedSkillsImportResult } from '../../api/sharedSkills';

function skill(overrides: Partial<SharedSkillView> = {}): SharedSkillView {
  return {
    name: 'skill-a',
    displayName: 'Skill A',
    description: '',
    version: '',
    author: '',
    enabled: true,
    path: '/home/u/.agents/skills/skill-a',
    hasSkillMd: true,
    managed: false,
    ...overrides,
  };
}

const IMPORTED_SKILL = skill({
  name: 'imported',
  displayName: 'Imported',
  enabled: false,
  path: '/home/u/.agents/skills-disabled/imported',
});

function envelope(overrides: Partial<SharedSkillsImportResult> = {}): SharedSkillsImportResult {
  return {
    imported: [IMPORTED_SKILL],
    skipped: [],
    views: [IMPORTED_SKILL],
    ...overrides,
  };
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('stores.sharedSkills 基础数据流', () => {
  it('load 成功替换列表并置 loaded；失败写 error 不 rethrow', async () => {
    const store = useSharedSkillsStore();
    apiMocks.listSharedSkills.mockResolvedValue([skill()]);
    await store.load();
    expect(store.skills).toHaveLength(1);
    expect(store.loaded).toBe(true);
    expect(store.error).toBe('');

    apiMocks.listSharedSkills.mockRejectedValue(new Error('boom'));
    await store.load();
    expect(store.error).toContain('boom');
  });

  it('setEnabled/remove 以响应列表整体替换', async () => {
    const store = useSharedSkillsStore();
    apiMocks.setSharedSkillEnabled.mockResolvedValue([skill({ enabled: false })]);
    await store.setEnabled('skill-a', false);
    expect(store.skills[0].enabled).toBe(false);

    apiMocks.deleteSharedSkill.mockResolvedValue([]);
    await store.remove('skill-a');
    expect(store.skills).toHaveLength(0);
  });
});

describe('stores.sharedSkills 导入动作', () => {
  it('取消选择（pick 空串）→ 返回 null 且不调导入', async () => {
    const store = useSharedSkillsStore();
    apiMocks.pickSharedSkillsZip.mockResolvedValue('');

    await expect(store.importFromZip()).resolves.toBeNull();
    expect(apiMocks.importSharedSkillsZip).not.toHaveBeenCalled();
    expect(store.importing).toBe(false);
  });

  it('zip 导入成功 → skills 替换为信封 views 并返回信封', async () => {
    const store = useSharedSkillsStore();
    store.skills = [skill()];
    apiMocks.pickSharedSkillsZip.mockResolvedValue('/tmp/demo.zip');
    apiMocks.importSharedSkillsZip.mockResolvedValue(envelope());

    const result = await store.importFromZip();
    expect(apiMocks.importSharedSkillsZip).toHaveBeenCalledWith('/tmp/demo.zip');
    expect(result?.imported).toHaveLength(1);
    expect(store.skills).toEqual([IMPORTED_SKILL]);
    expect(store.importing).toBe(false);
  });

  it('文件夹导入走 Dir 通道', async () => {
    const store = useSharedSkillsStore();
    apiMocks.pickSharedSkillsDir.mockResolvedValue('/tmp/skills-src');
    apiMocks.importSharedSkillsDir.mockResolvedValue(envelope());

    const result = await store.importFromFolder();
    expect(apiMocks.importSharedSkillsDir).toHaveBeenCalledWith('/tmp/skills-src');
    expect(result?.views).toEqual([IMPORTED_SKILL]);
  });

  it('导入失败 → rethrow 且列表不动', async () => {
    const store = useSharedSkillsStore();
    store.skills = [skill()];
    apiMocks.pickSharedSkillsZip.mockResolvedValue('/tmp/bad.zip');
    apiMocks.importSharedSkillsZip.mockRejectedValue(new Error('路径穿越'));

    await expect(store.importFromZip()).rejects.toThrow('路径穿越');
    expect(store.skills).toHaveLength(1);
    expect(store.importing).toBe(false);
  });
});
