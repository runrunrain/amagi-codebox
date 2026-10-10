import { describe, expect, it } from 'vitest';
import {
  applyCompactionMode, compactionModeOf, compactionModelOf, validateCompactionConfig,
} from '../../../components/provider/piCompaction';

describe('amagi-pi compaction configuration', () => {
  it('distinguishes inheritance, explicit native model and custom model', () => {
    expect(compactionModeOf(undefined)).toBe('unset');
    expect(compactionModeOf({})).toBe('unset');
    expect(compactionModeOf({ model: null })).toBe('native');
    expect(compactionModeOf({ model: 'amagi-glm/glm-5.3:max' })).toBe('custom');
    expect(compactionModelOf({ model: 'amagi-glm/glm-5.3:max' })).toBe('amagi-glm/glm-5.3:max');
    expect(compactionModelOf({ model: null })).toBe('');
  });

  it('copies an exact luban spec without changing role, MCP, permissions or future config', () => {
    const config = {
      agents: { luban: { model: 'amagi-glm/glm-5.3:max' } },
      mcp: { leader: [], agents: { luban: ['web-search'] } },
      permissions: { deny: ['/private'] },
      futureOption: { enabled: true },
    };
    const original = JSON.parse(JSON.stringify(config));
    applyCompactionMode(config, 'custom', config.agents.luban.model);
    const saved = JSON.parse(JSON.stringify(config));
    expect(saved.compaction).toEqual({ model: 'amagi-glm/glm-5.3:max' });
    expect(validateCompactionConfig(saved)).toBe('');
    delete saved.compaction;
    expect(saved).toEqual(original);
    config.agents.luban.model = 'other/work:high';
    expect(compactionModelOf((config as Record<string, unknown>).compaction)).toBe('amagi-glm/glm-5.3:max');
  });

  it('round-trips custom -> null -> deleted without auto-selecting a model', () => {
    const config: Record<string, unknown> = {};
    applyCompactionMode(config, 'custom');
    expect(compactionModeOf(config.compaction)).toBe('custom');
    expect(validateCompactionConfig(config)).toMatch(/完整/);
    applyCompactionMode(config, 'custom', 'ollama/qwen:32b:high');
    expect(validateCompactionConfig(config)).toBe('');
    applyCompactionMode(config, 'native');
    expect(JSON.stringify(config)).toBe('{"compaction":{"model":null}}');
    applyCompactionMode(config, 'unset');
    expect(JSON.stringify(config)).toBe('{}');
  });

  it('accepts native model IDs without requiring a local catalogue entry', () => {
    for (const model of ['dynamic/unknown', 'openrouter/anthropic/model:high', 'ollama/qwen:32b', 'ollama/qwen:32b:max']) {
      expect(validateCompactionConfig({ compaction: { model } })).toBe('');
    }
    expect(validateCompactionConfig({})).toBe('');
    expect(validateCompactionConfig({ compaction: {} })).toBe('');
    expect(validateCompactionConfig({ compaction: { model: null } })).toBe('');
  });

  it('rejects malformed shapes and incomplete specs, without changing input', () => {
    const values = [null, [], 'oops', { model: 3 }, { model: '' }, { model: 'p/' },
      { model: 'p/:high' }, { model: '/m' }, { model: 'p/m high' }, { model: ' p/m' },
      { model: 'p/m\uFEFF' }, { model: 'p/m', apiKey: 'secret-fixture' }];
    for (const compaction of values) {
      const config = { compaction };
      const before = JSON.stringify(config);
      expect(validateCompactionConfig(config)).toMatch(/compaction/);
      expect(validateCompactionConfig(config)).not.toContain('secret-fixture');
      expect(JSON.stringify(config)).toBe(before);
    }
  });

  it('does not silently delete unknown compaction fields or replace invalid values', () => {
    const config: Record<string, unknown> = { compaction: { model: 'p/m', future: 1 } };
    applyCompactionMode(config, 'unset');
    expect(config.compaction).toEqual({ future: 1 });
    expect(validateCompactionConfig(config)).toMatch(/只允许 model/);
    const invalid = { compaction: ['preserve-me'] };
    expect(() => applyCompactionMode(invalid, 'native')).toThrow(/JSON/);
    expect(invalid.compaction).toEqual(['preserve-me']);
  });
});
