import { describe, it, expect } from 'vitest';
import {
  normalizeLimitInput,
  cleanConcurrencyConfig,
  buildProviderDropdownOptions,
  buildModelDropdownOptions,
} from '../../../components/provider/piConcurrency';
import type { ModelCatalog } from '../../../components/provider/useModelCatalog';

describe('piConcurrency helpers', () => {
  describe('normalizeLimitInput (Problem 1 & 2)', () => {
    it('handles numeric input without throwing TypeError', () => {
      // TextInput emits numbers directly when type="number" in some contexts
      expect(normalizeLimitInput(12)).toBe(12);
      expect(normalizeLimitInput(0)).toBe('0');
      expect(normalizeLimitInput(-5)).toBe('-5');
    });

    it('handles string input and trims whitespace', () => {
      expect(normalizeLimitInput(' 8 ')).toBe(8);
      expect(normalizeLimitInput('16')).toBe(16);
      expect(normalizeLimitInput('')).toBe('');
      expect(normalizeLimitInput('   ')).toBe('');
    });

    it('handles null and undefined safely', () => {
      expect(normalizeLimitInput(null)).toBe('');
      expect(normalizeLimitInput(undefined)).toBe('');
    });

    it('preserves string intermediate state when not a positive integer', () => {
      expect(normalizeLimitInput('abc')).toBe('abc');
      expect(normalizeLimitInput('0')).toBe('0');
      expect(normalizeLimitInput('-1')).toBe('-1');
    });
  });

  describe('cleanConcurrencyConfig (Problem 2 save cleanup contract)', () => {
    it('cleans empty strings and invalid limits on save, converting valid strings to numbers', () => {
      const input = {
        default: '6',
        providers: {
          openrouter: '8',
          emptyProvider: '',
          whitespaceProvider: '   ',
          invalidProvider: '-1',
        },
        models: {
          'anthropic/claude-3-7-sonnet': '3',
          'openai/gpt-4o': '',
          'invalid-no-slash': '5',
        },
      };

      const result = cleanConcurrencyConfig(input);
      expect(result).toEqual({
        default: 6,
        providers: {
          openrouter: 8,
        },
        models: {
          'anthropic/claude-3-7-sonnet': 3,
        },
      });
    });

    it('removes concurrency entirely when all fields are empty or invalid', () => {
      const allEmpty = {
        default: '',
        providers: {
          p1: '',
        },
        models: {
          'm/1': '',
        },
      };
      expect(cleanConcurrencyConfig(allEmpty)).toBeUndefined();
    });

    it('removes empty sub-objects (providers or models) while keeping valid default', () => {
      const onlyDefault = {
        default: '4',
        providers: {
          p1: '',
        },
        models: {},
      };
      const result = cleanConcurrencyConfig(onlyDefault);
      expect(result).toEqual({
        default: 4,
      });
      expect(result?.providers).toBeUndefined();
      expect(result?.models).toBeUndefined();
    });
  });

  describe('buildProviderDropdownOptions (Problem 3)', () => {
    const mockCatalog: ModelCatalog = {
      providers: [
        { name: 'anthropic', models: [{ id: 'claude-3-5-sonnet', reasoning: false }] },
        { name: 'openai', models: [{ id: 'gpt-4o', reasoning: false }] },
      ],
    };

    it('unions catalog providers, agent model providers, and existing concurrency providers with stable sort', () => {
      const agentModels = ['deepseek/deepseek-chat', 'anthropic/claude-3-7-sonnet:high'];
      const existingProviders = { openrouter: 8 };

      const options = buildProviderDropdownOptions(mockCatalog, agentModels, existingProviders);
      const values = options.map((o) => o.value);

      // Union: anthropic, deepseek, openai, openrouter + __custom__
      expect(values).toEqual([
        'anthropic',
        'deepseek',
        'openai',
        'openrouter',
        '__custom__',
      ]);
      expect(options.find((o) => o.value === '__custom__')?.label).toBe('＋ 自定义服务商...');
    });

    it('handles empty/null sources gracefully', () => {
      const options = buildProviderDropdownOptions(null, [], null);
      expect(options).toEqual([{ value: '__custom__', label: '＋ 自定义服务商...' }]);
    });
  });

  describe('buildModelDropdownOptions (Problem 3)', () => {
    const mockCatalog: ModelCatalog = {
      providers: [
        {
          name: 'anthropic',
          models: [
            { id: 'claude-3-5-sonnet', reasoning: false },
            { id: 'claude-3-7-sonnet', reasoning: true },
          ],
        },
      ],
    };

    it('unions catalog models, agent models (stripping thinking level), and existing keys with slash filter and stable sort', () => {
      const agentModels = [
        'deepseek/deepseek-r1:medium',
        'invalid-spec-without-slash',
        'anthropic/claude-3-7-sonnet:high',
      ];
      const existingModels = {
        'openai/gpt-4o': 2,
        'bad-model-no-slash': 1,
      };

      const options = buildModelDropdownOptions(mockCatalog, agentModels, existingModels);
      const values = options.map((o) => o.value);

      expect(values).toEqual([
        'anthropic/claude-3-5-sonnet',
        'anthropic/claude-3-7-sonnet',
        'deepseek/deepseek-r1',
        'openai/gpt-4o',
        '__custom__',
      ]);
      expect(options.find((o) => o.value === '__custom__')?.label).toBe('＋ 自定义模型...');
    });
  });
});
