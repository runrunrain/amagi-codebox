import { describe, expect, it } from 'vitest';
import { config, piconfig } from '../../../../wailsjs/go/models';
import PiLoginPresetDialog from '../../../components/provider/PiLoginPresetDialog.vue';
import { renderWithCapture } from '../extensions/ui/testUtils';

const source = (name: string) => new piconfig.PiLoginProvider({
  id: `pi-login/${name}`, provider: name, cli: 'pi', models: [],
});

async function renderDialog(props: Record<string, unknown> = {}) {
  const { teleports } = await renderWithCapture(PiLoginPresetDialog, {
    open: true, preset: null, preferredProvider: '', providers: [], canAdd: false, presets: {}, ...props,
  });
  return teleports.body;
}

// Server-rendered assertions cover the OAuth form contract without requiring a DOM library or credentials.
describe('Pi login preset editor', () => {
  it('preselects the login card source but never shows API/provider sync fields', async () => {
    const html = await renderDialog({
      preferredProvider: 'second-source', providers: [source('first-source'), source('second-source')], canAdd: true,
    });
    expect(html).toContain('添加 Pi 登录预设');
    expect(html).toMatch(/<option value="second-source"[^>]* selected>/);
    expect(html).toContain('暂无本地模型目录，可手动填写');
    expect(html).not.toMatch(/Base URL|Temperature|导出到识图/);
  });

  it('still exposes the saved model and shared limits for editing without a login source', async () => {
    const preset = new config.PiLoginPreset({
      name: '日常', provider: 'first-source', model: 'native-model', thinking: 'max',
      context_window: 256001, max_tokens: 32768,
    });
    const html = await renderDialog({ preset });
    expect(html).toContain('编辑 Pi 登录预设');
    expect(html).toMatch(/<input[^>]*value="日常"[^>]*readonly/);
    expect(html).toMatch(/<input[^>]*value="first-source"[^>]*readonly/);
    expect(html).toContain('value="native-model"');
    expect(html).toContain('value="256001"');
    expect(html).toContain('value="32768"');
    expect(html).toMatch(/<option value="max"[^>]* selected>/);
    expect(html).not.toMatch(/<button[^>]*type="submit"[^>]*disabled/);
  });
});
