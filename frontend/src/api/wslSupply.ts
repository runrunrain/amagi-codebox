/**
 * WSL Config Supply API (P3-S1)
 * Minimal status surface for the「配置状态」section under the WSL CLI card:
 * per-face (providers / vision export / modality KB / OpenCode layer) sync
 * state and the per-target vision-export consent entry (key cross-boundary
 * authorization). Wraps the App-bound methods GetWSLConfigSupplyStatus /
 * GrantWSLVisionConsent (generated bindings, never hand-edited).
 */

import { GetWSLConfigSupplyStatus, GrantWSLVisionConsent } from '../../wailsjs/go/main/App';
import { main } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type WSLConfigSupplyStatus = main.WSLConfigSupplyStatus;
export type WSLConfigSupplyFace = main.WSLConfigSupplyFace;

/**
 * Read the sanitized WSL config-supply status snapshot (cache read only: never
 * triggers probing, installation or sync).
 */
export function getWSLConfigSupplyStatus(): Promise<WSLConfigSupplyStatus> {
  return callApi('[api.wslSupply.getWSLConfigSupplyStatus]', () => GetWSLConfigSupplyStatus());
}

/**
 * Grant the per-target consent to share the vision export (key copy) into the
 * current WSL distro. Idempotent; schedules one async push afterwards.
 */
export function grantWSLVisionConsent(): Promise<void> {
  return callApi('[api.wslSupply.grantWSLVisionConsent]', () => GrantWSLVisionConsent());
}
