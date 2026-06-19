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

type UpdateInfo = updater.UpdateInfo;

/**
 * Check for updates
 */
export async function checkForUpdate(): Promise<UpdateInfo | null> {
  try {
    return await CheckForUpdate();
  } catch (error) {
    console.error('Failed to check for updates:', error);
    throw error;
  }
}

/**
 * Download and apply update
 */
export async function downloadAndApplyUpdate(): Promise<void> {
  try {
    await DownloadAndApplyUpdate();
  } catch (error) {
    console.error('Failed to download update:', error);
    throw error;
  }
}

/**
 * Get GitHub token
 */
export async function getUpdaterGitHubToken(): Promise<string> {
  try {
    return await GetGitHubToken();
  } catch (error) {
    console.error('Failed to get GitHub token:', error);
    throw error;
  }
}

/**
 * Set GitHub token
 */
export async function setUpdaterGitHubToken(token: string): Promise<void> {
  try {
    await SetGitHubToken(token);
  } catch (error) {
    console.error('Failed to set GitHub token:', error);
    throw error;
  }
}
