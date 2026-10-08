import { GetPiLoginProviders } from '../../wailsjs/go/piconfig/Service';
import { GetPiLoginPresets, SavePiLoginPreset, DeletePiLoginPreset } from '../../wailsjs/go/config/ConfigService';
import { config, piconfig } from '../../wailsjs/go/models';

export type PiLoginProvider = piconfig.PiLoginProvider;
export type PiLoginPreset = config.PiLoginPreset;
export const getPiLoginProviders = GetPiLoginProviders;
export const getPiLoginPresets = GetPiLoginPresets;
export const savePiLoginPreset = SavePiLoginPreset;
export const deletePiLoginPreset = DeletePiLoginPreset;

/** Dedicated login presets are projected ONLY into the Pi selection, never public buckets. */
export function piLoginSelections(providers: PiLoginProvider[], presets: Record<string, PiLoginPreset>) {
  return Object.entries(presets).flatMap(([key, preset]) => {
    const source = providers.find(p => p.provider === preset.provider);
    return source ? [new config.MergedTerminalPreset({
      key, provider: source.id, model: preset.model, label: preset.name,
      source: 'pi-login', name: preset.name,
    })] : [];
  });
}
