/**
 * Proxy API
 * Encapsulates proxy/injection operations.
 *
 * M3-A2: raw proxy.ProxyService is removed from the Wails Bind (C-01); all
 * mutations now go through App-level facade methods (ProxyStart/Stop/etc.)
 * which are lease-guarded by the SharedServiceCoordinator (design §6.7).
 */

import {
  ProxyGetRules,
  ProxySetRules,
  ProxyAddRule,
  ProxyUpdateRule,
  ProxyDeleteRule,
  ProxyLoadRules,
  ProxySaveRules,
  GetProxyBackendURLHistory,
  AddProxyBackendURL,
  RemoveProxyBackendURL,
  SetProxyBackendURL,
  ProxyStart,
  ProxyStop,
  ProxyIsRunning,
  ProxyGetStatus,
  ProxyGetLogs,
  ProxyGetPort,
} from '../../wailsjs/go/main/App';

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
    return await ProxyGetStatus();
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
    return await ProxyIsRunning();
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
    return await ProxyGetPort();
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
    await ProxyStart(port, backendUrl);
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
    await ProxyStop();
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
    return await ProxyGetRules();
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
    await ProxySetRules(rules);
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
    await ProxyAddRule(rule);
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
    await ProxyUpdateRule(rule);
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
    await ProxyDeleteRule(id);
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
    await ProxyLoadRules(configDir);
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
    await ProxySaveRules(configDir);
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
    return await GetProxyBackendURLHistory();
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
    await AddProxyBackendURL(url);
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
    await RemoveProxyBackendURL(url);
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
    await SetProxyBackendURL(url);
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
    return await ProxyGetLogs();
  } catch (error) {
    console.error('[api.proxy.getProxyLogs]', error);
    throw error;
  }
}
