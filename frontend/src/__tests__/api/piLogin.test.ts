import { describe, it, expect } from 'vitest';
import { config, piconfig } from '../../../wailsjs/go/models';
import { piLoginSelections } from '../../api/piLogin';

describe('Pi-only login selection projection', () => {
  const provider = new piconfig.PiLoginProvider({ id: 'pi-login/b3BlbmFpLWNvZGV4', provider: 'openai-codex', cli: 'pi', source: '~/.pi/agent/auth.json', models: [] });
  const preset = new config.PiLoginPreset({ name: 'Daily', provider: 'openai-codex', model: 'native-model', thinking: 'max' });
  const key = `${provider.id}/RGFpbHk`;
  it('keeps the source namespace and stable preset key, even with a missing model catalog', () => {
    const entries = piLoginSelections([provider], { [key]: preset });
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({ key, provider: provider.id, model: 'native-model', label: 'Daily', source: 'pi-login' });
    expect(entries[0].provider).not.toBe(preset.provider); // ordinary API provider namesake remains distinct
  });
  it('does not offer a persisted preset after logout or on another host', () => {
    expect(piLoginSelections([], { [key]: preset })).toEqual([]);
    expect(piLoginSelections([provider], { other: new config.PiLoginPreset({ ...preset, provider: 'other' }) })).toEqual([]);
  });
});
