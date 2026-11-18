import { GetLegacySharedExtensionsSummary } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';

export type LegacySharedExtensionsSummary = main.LegacySharedExtensionsSummary;

export function getLegacySharedExtensionsSummary(): Promise<LegacySharedExtensionsSummary> {
  return GetLegacySharedExtensionsSummary();
}
