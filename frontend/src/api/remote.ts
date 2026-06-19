/**
 * Remote API
 * Encapsulates remote control operations
 */

import {
  GetRemoteToken,
  GetRemoteStatus,
  GetRemoteWebUIStatus,
  OpenRemoteWebUI,
  RegenerateRemoteToken,
  ToggleRemoteServer,
  SetRemotePort,
  SetRemoteHost,
} from '../../wailsjs/go/main/App';

/**
 * Get remote token
 */
export async function getRemoteToken(): Promise<string> {
  try {
    return await GetRemoteToken();
  } catch (error) {
    console.error('Failed to get remote token:', error);
    throw error;
  }
}

/**
 * Get remote status
 */
export async function getRemoteStatus(): Promise<Record<string, any>> {
  try {
    return await GetRemoteStatus();
  } catch (error) {
    console.error('Failed to get remote status:', error);
    throw error;
  }
}

/**
 * Get remote Web UI status
 */
export async function getRemoteWebUIStatus(): Promise<any> {
  try {
    return await GetRemoteWebUIStatus();
  } catch (error) {
    console.error('Failed to get remote Web UI status:', error);
    throw error;
  }
}

/**
 * Open remote Web UI
 */
export async function openRemoteWebUI(): Promise<any> {
  try {
    return await OpenRemoteWebUI();
  } catch (error) {
    console.error('Failed to open remote Web UI:', error);
    throw error;
  }
}

/**
 * Regenerate remote token
 */
export async function regenerateRemoteToken(): Promise<string> {
  try {
    return await RegenerateRemoteToken();
  } catch (error) {
    console.error('Failed to regenerate remote token:', error);
    throw error;
  }
}

/**
 * Toggle remote server
 */
export async function toggleRemoteServer(enabled: boolean): Promise<void> {
  try {
    await ToggleRemoteServer(enabled);
  } catch (error) {
    console.error('Failed to toggle remote server:', error);
    throw error;
  }
}

/**
 * Set remote port
 */
export async function setRemotePort(port: number): Promise<void> {
  try {
    await SetRemotePort(port);
  } catch (error) {
    console.error('Failed to set remote port:', error);
    throw error;
  }
}

/**
 * Set remote host
 */
export async function setRemoteHost(host: string): Promise<void> {
  try {
    await SetRemoteHost(host);
  } catch (error) {
    console.error('Failed to set remote host:', error);
    throw error;
  }
}
