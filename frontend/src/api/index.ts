/**
 * API Index
 * Centralized re-exports of all API modules.
 *
 * Note: settings and remote both expose setRemoteHost / setRemotePort.
 * They are explicitly re-exported here under namespaced names to avoid
 * star-export collisions.
 */

// Session & Terminal
export * from './session';

// Provider & Preset
export * from './provider';

// Plugins
export * from './plugin';
export * from './codexPlugin';

// Workspace
export * from './workspace';

// Proxy & Injection
export * from './proxy';

// Settings (host/port setters namespaced to avoid collision with remote)
export {
  getDashboardDefaults,
  setDashboardDefaults,
  getShellPaths,
  addShellPath,
  removeShellPath,
  getTerminalSettings,
  setTerminalSettings,
  getGitHubToken,
  setGitHubToken,
  getMobileWebRoot,
  setMobileWebRoot,
  getAllSettings,
  loadSettings,
  saveSettings,
} from './settings';
export {
  getRemoteHost as getSettingsRemoteHost,
  getRemotePort as getSettingsRemotePort,
  setRemoteHost as setSettingsRemoteHost,
  setRemotePort as setSettingsRemotePort,
} from './settings';

// Environment Check
export * from './envcheck';

// Environment Variables
export * from './envvars';

// Logs
export * from './logs';

// Remote Control (host/port setters namespaced)
export {
  getRemoteToken,
  getRemoteStatus,
  getRemoteWebUIStatus,
  openRemoteWebUI,
  regenerateRemoteToken,
  toggleRemoteServer,
} from './remote';
export {
  setRemotePort,
  setRemoteHost,
} from './remote';

// Updates
export * from './updater';

// Paths
export * from './paths';

// Usage Statistics (AI model usage & cost)
export * from './usage';
