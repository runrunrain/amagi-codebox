/**
 * Updater API
 * Encapsulates software update operations
 */

import {
  CheckForUpdate,
  DownloadAndApplyUpdate,
  GetGitHubToken,
  SetGitHubToken,
} from '../../wailsjs/go/main/App';

import { updater } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

type UpdateInfo = updater.UpdateInfo;

/**
 * Check for updates
 */
export function checkForUpdate(): Promise<UpdateInfo | null> {
  return callApi('[api.updater.checkForUpdate]', () => CheckForUpdate());
}

/**
 * Download and apply update
 */
export function downloadAndApplyUpdate(): Promise<void> {
  return callApi('[api.updater.downloadAndApplyUpdate]', () => DownloadAndApplyUpdate());
}

/**
 * Get GitHub token
 */
export function getUpdaterGitHubToken(): Promise<string> {
  return callApi('[api.updater.getUpdaterGitHubToken]', () => GetGitHubToken());
}

/**
 * Set GitHub token
 */
export function setUpdaterGitHubToken(token: string): Promise<void> {
  return callApi('[api.updater.setUpdaterGitHubToken]', () => SetGitHubToken(token));
}
