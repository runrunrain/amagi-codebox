import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  EDITOR_MODE_OPTIONS,
  copyConfigPath,
  genId,
  hexToRgb,
} from '../components/provider/editorUtils';

describe('provider/editorUtils', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  describe('EDITOR_MODE_OPTIONS', () => {
    it('exposes shared visual and JSON mode options', () => {
      expect(EDITOR_MODE_OPTIONS).toEqual([
        { value: 'visual', label: '可视化' },
        { value: 'json', label: 'JSON' },
      ]);
    });
  });

  describe('genId', () => {
    it('generates non-empty unique IDs without prefix', () => {
      const ids = Array.from({ length: 50 }, () => genId());
      for (const id of ids) {
        expect(typeof id).toBe('string');
        expect(id.length).toBeGreaterThan(0);
      }
      expect(new Set(ids).size).toBe(50);
    });

    it('prepends prefix when provided and ignores empty prefix', () => {
      const a = genId('p-row');
      const b = genId('p-row');
      expect(a.startsWith('p-row-')).toBe(true);
      expect(b.startsWith('p-row-')).toBe(true);
      expect(a).not.toBe(b);

      const emptyPrefixed = genId('');
      expect(emptyPrefixed.length).toBeGreaterThan(0);
      expect(emptyPrefixed.startsWith('-')).toBe(false);
    });

    it('falls back cleanly to unique non-empty IDs when crypto.randomUUID is unavailable', () => {
      vi.stubGlobal('crypto', {});

      const ids = Array.from({ length: 20 }, () => genId());
      for (const id of ids) {
        expect(id.startsWith('id-')).toBe(true);
        expect(id.length).toBeGreaterThan(3);
      }
      expect(new Set(ids).size).toBe(20);

      const prefixedA = genId('m-row');
      const prefixedB = genId('m-row');
      expect(prefixedA.startsWith('m-row-')).toBe(true);
      expect(prefixedB.startsWith('m-row-')).toBe(true);
      expect(prefixedA).not.toBe(prefixedB);
    });
  });

  describe('hexToRgb', () => {
    it('parses 3-character hex colors (case-insensitive)', () => {
      expect(hexToRgb('#fff')).toEqual({ r: 255, g: 255, b: 255 });
      expect(hexToRgb('#000')).toEqual({ r: 0, g: 0, b: 0 });
      expect(hexToRgb('#09C')).toEqual({ r: 0, g: 153, b: 204 });
    });

    it('parses 6-character hex colors (case-insensitive)', () => {
      expect(hexToRgb('#3b82f6')).toEqual({ r: 59, g: 130, b: 246 });
      expect(hexToRgb('#34C759')).toEqual({ r: 52, g: 199, b: 89 });
      expect(hexToRgb('#000000')).toEqual({ r: 0, g: 0, b: 0 });
    });

    it('returns null for invalid hex strings', () => {
      const invalidInputs = [
        '',
        '3b82f6',
        'fff',
        '#',
        '#12',
        '#1234',
        '#12345',
        '#1234567',
        '#ggg',
        '#gggggg',
        'rgb(59, 130, 246)',
        'not-a-color',
        ' #3b82f6',
      ];
      for (const input of invalidInputs) {
        expect(hexToRgb(input)).toBeNull();
      }
    });
  });

  describe('copyConfigPath', () => {
    it('returns early without touching clipboard or toast for empty/undefined paths', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined);
      vi.stubGlobal('navigator', { clipboard: { writeText } });
      const toast = { showSuccess: vi.fn(), showError: vi.fn() };

      await copyConfigPath(undefined, toast);
      await copyConfigPath('', toast);
      await copyConfigPath('   ', toast);

      expect(writeText).not.toHaveBeenCalled();
      expect(toast.showSuccess).not.toHaveBeenCalled();
      expect(toast.showError).not.toHaveBeenCalled();
    });

    it('copies path via navigator.clipboard.writeText and shows success toast', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined);
      vi.stubGlobal('navigator', { clipboard: { writeText } });
      const toast = { showSuccess: vi.fn(), showError: vi.fn() };

      await copyConfigPath('C:/Users/demo/.pi/agent/amagi.json', toast);

      expect(writeText).toHaveBeenCalledTimes(1);
      expect(writeText).toHaveBeenCalledWith('C:/Users/demo/.pi/agent/amagi.json');
      expect(toast.showSuccess).toHaveBeenCalledWith('路径已复制到剪贴板');
      expect(toast.showError).not.toHaveBeenCalled();
    });

    it('shows error toast when navigator.clipboard.writeText rejects and no DOM fallback is available', async () => {
      const writeText = vi.fn().mockRejectedValue(new Error('permission denied'));
      vi.stubGlobal('navigator', { clipboard: { writeText } });
      const toast = { showSuccess: vi.fn(), showError: vi.fn() };

      await copyConfigPath('/etc/opencode/config.json', toast);

      expect(writeText).toHaveBeenCalledWith('/etc/opencode/config.json');
      expect(toast.showSuccess).not.toHaveBeenCalled();
      expect(toast.showError).toHaveBeenCalledWith('复制失败');
    });

    it('shows error toast when clipboard API is unavailable and no DOM fallback exists', async () => {
      vi.stubGlobal('navigator', {});
      const toast = { showSuccess: vi.fn(), showError: vi.fn() };

      await copyConfigPath('/tmp/models.json', toast);

      expect(toast.showSuccess).not.toHaveBeenCalled();
      expect(toast.showError).toHaveBeenCalledWith('复制失败');
    });

    it('falls back to textarea + execCommand when Clipboard API rejects in a DOM environment', async () => {
      const writeText = vi.fn().mockRejectedValue(new Error('blocked'));
      vi.stubGlobal('navigator', { clipboard: { writeText } });

      const removeChild = vi.fn();
      const fakeTextarea = {
        value: '',
        style: {} as Record<string, string>,
        parentNode: { removeChild },
        setAttribute: vi.fn(),
        select: vi.fn(),
      };
      const appendChild = vi.fn();
      const execCommand = vi.fn().mockReturnValue(true);
      vi.stubGlobal('document', {
        body: { appendChild },
        createElement: vi.fn().mockReturnValue(fakeTextarea),
        execCommand,
      });

      const toast = { showSuccess: vi.fn(), showError: vi.fn() };
      await copyConfigPath('/home/user/.config/opencode/opencode.json', toast);

      expect(fakeTextarea.value).toBe('/home/user/.config/opencode/opencode.json');
      expect(fakeTextarea.select).toHaveBeenCalledTimes(1);
      expect(execCommand).toHaveBeenCalledWith('copy');
      expect(removeChild).toHaveBeenCalledWith(fakeTextarea);
      expect(toast.showSuccess).toHaveBeenCalledWith('路径已复制到剪贴板');
      expect(toast.showError).not.toHaveBeenCalled();
    });

    it('shows error toast and cleans up textarea when both Clipboard API and execCommand fail', async () => {
      const writeText = vi.fn().mockRejectedValue(new Error('blocked'));
      vi.stubGlobal('navigator', { clipboard: { writeText } });

      const removeChild = vi.fn();
      const fakeTextarea = {
        value: '',
        style: {} as Record<string, string>,
        parentNode: { removeChild },
        setAttribute: vi.fn(),
        select: vi.fn(),
      };
      vi.stubGlobal('document', {
        body: { appendChild: vi.fn() },
        createElement: vi.fn().mockReturnValue(fakeTextarea),
        execCommand: vi.fn().mockReturnValue(false),
      });

      const toast = { showSuccess: vi.fn(), showError: vi.fn() };
      await copyConfigPath('/home/user/.pi/agent/auth.json', toast);

      expect(removeChild).toHaveBeenCalledWith(fakeTextarea);
      expect(toast.showSuccess).not.toHaveBeenCalled();
      expect(toast.showError).toHaveBeenCalledWith('复制失败');
    });
  });
});
