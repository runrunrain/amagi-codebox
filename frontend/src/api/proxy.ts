/**
 * Proxy API
 * Encapsulates proxy/injection operations
 * Directly wraps wailsjs/go/proxy/ProxyService
 */

import {
  GetRules,
  SetRules,
  AddRule,
  UpdateRule,
  DeleteRule,
  LoadRules,
  SaveRules,
  GetBackendURLHistory,
  AddBackendURL,
  RemoveBackendURL,
  SetBackendURL,
  Start,
  Stop,
  IsRunning,
  GetStatus,
  GetLogs,
  GetPort,
} from '../../wailsjs/go/proxy/ProxyService';

import { proxy } from '../../wailsjs/go/models';

// Type aliases
type InjectionRule = proxy.InjectionRule;
type InjectionLog = proxy.InjectionLog;
type ProxyStatus = proxy.ProxyStatus;

/**
 * Get proxy status
 */
export async function getProxyStatus(): Promise<ProxyStatus> {
  try {
    return await GetStatus();
  } catch (error) {
    console.error('[api.proxy.getProxyStatus]', error);
    throw error;
  }
}

/**
 * Check if proxy is running
 */
export async function isProxyRunning(): Promise<boolean> {
  try {
    return await IsRunning();
  } catch (error) {
    console.error('[api.proxy.isProxyRunning]', error);
    throw error;
  }
}

/**
 * Get proxy port
 */
export async function getProxyPort(): Promise<number> {
  try {
    return await GetPort();
  } catch (error) {
    console.error('[api.proxy.getProxyPort]', error);
    throw error;
  }
}

/**
 * Start proxy
 */
export async function startProxy(port: number, backendUrl: string): Promise<void> {
  try {
    await Start(port, backendUrl);
  } catch (error) {
    console.error('[api.proxy.startProxy]', error);
    throw error;
  }
}

/**
 * Stop proxy
 */
export async function stopProxy(): Promise<void> {
  try {
    await Stop();
  } catch (error) {
    console.error('[api.proxy.stopProxy]', error);
    throw error;
  }
}

/**
 * Get injection rules
 */
export async function getProxyRules(): Promise<InjectionRule[]> {
  try {
    return await GetRules();
  } catch (error) {
    console.error('[api.proxy.getProxyRules]', error);
    throw error;
  }
}

/**
 * Set injection rules
 */
export async function setProxyRules(rules: InjectionRule[]): Promise<void> {
  try {
    await SetRules(rules);
  } catch (error) {
    console.error('[api.proxy.setProxyRules]', error);
    throw error;
  }
}

/**
 * Add injection rule
 */
export async function addProxyRule(rule: InjectionRule): Promise<void> {
  try {
    await AddRule(rule);
  } catch (error) {
    console.error('[api.proxy.addProxyRule]', error);
    throw error;
  }
}

/**
 * Update injection rule
 */
export async function updateProxyRule(rule: InjectionRule): Promise<void> {
  try {
    await UpdateRule(rule);
  } catch (error) {
    console.error('[api.proxy.updateProxyRule]', error);
    throw error;
  }
}

/**
 * Delete injection rule
 */
export async function deleteProxyRule(id: string): Promise<void> {
  try {
    await DeleteRule(id);
  } catch (error) {
    console.error('[api.proxy.deleteProxyRule]', error);
    throw error;
  }
}

/**
 * Load rules from config
 */
export async function loadProxyRules(configDir: string): Promise<void> {
  try {
    await LoadRules(configDir);
  } catch (error) {
    console.error('[api.proxy.loadProxyRules]', error);
    throw error;
  }
}

/**
 * Save rules to config
 */
export async function saveProxyRules(configDir: string): Promise<void> {
  try {
    await SaveRules(configDir);
  } catch (error) {
    console.error('[api.proxy.saveProxyRules]', error);
    throw error;
  }
}

/**
 * Get backend URL history
 */
export async function getBackendURLHistory(): Promise<string[]> {
  try {
    return await GetBackendURLHistory();
  } catch (error) {
    console.error('[api.proxy.getBackendURLHistory]', error);
    throw error;
  }
}

/**
 * Add backend URL
 */
export async function addBackendURL(url: string): Promise<void> {
  try {
    await AddBackendURL(url);
  } catch (error) {
    console.error('[api.proxy.addBackendURL]', error);
    throw error;
  }
}

/**
 * Remove backend URL
 */
export async function removeBackendURL(url: string): Promise<void> {
  try {
    await RemoveBackendURL(url);
  } catch (error) {
    console.error('[api.proxy.removeBackendURL]', error);
    throw error;
  }
}

/**
 * Set backend URL
 */
export async function setBackendURL(url: string): Promise<void> {
  try {
    await SetBackendURL(url);
  } catch (error) {
    console.error('[api.proxy.setBackendURL]', error);
    throw error;
  }
}

/**
 * Get proxy logs
 */
export async function getProxyLogs(): Promise<InjectionLog[]> {
  try {
    return await GetLogs();
  } catch (error) {
    console.error('[api.proxy.getProxyLogs]', error);
    throw error;
  }
}
