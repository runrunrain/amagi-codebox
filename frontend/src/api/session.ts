/**
 * Session API
 * Encapsulates Wails session operations
 */

import {
  LaunchSession,
  LaunchOpenCode,
  LaunchCodexSession,
  LaunchPiSession,
  LaunchOmpSession,
  StopSession,
  GetSessions,
  RemoveSession
} from '../../wailsjs/go/main/App';

import { session } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type alias
type SessionInfo = session.SessionInfo;

/**
 * Launch a Claude Code session
 */
export function launchClaudeSession(params: {
  providerName: string;
  presetName: string;
  mode: string;
  workDir: string;
  shellPath?: string;
}): Promise<string> {
  return callApi('[api.session.launchClaudeSession]', () => LaunchSession(
    params.providerName,
    params.presetName,
    params.mode,
    params.workDir,
    params.shellPath || ''
  ));
}

/**
 * Launch an OpenCode session
 */
export function launchOpenCodeSession(params: {
  providerName: string;
  presetName: string;
  mode: string;
  workDir: string;
  shellPath?: string;
}): Promise<string> {
  return callApi('[api.session.launchOpenCodeSession]', () => LaunchOpenCode(
    params.providerName,
    params.presetName,
    params.mode,
    params.workDir,
    params.shellPath || ''
  ));
}

/**
 * Launch a Codex CLI session
 */
export function launchCodexSession(params: {
  modelName: string;
  providerID: string;
  mode: string;
  workDir: string;
  shellPath?: string;
}): Promise<string> {
  return callApi('[api.session.launchCodexSession]', () => LaunchCodexSession(
    params.modelName,
    params.providerID,
    params.mode,
    params.workDir,
    params.shellPath || ''
  ));
}

/**
 * Launch a Pi coding agent session
 */
export function launchPiSession(params: {
  modelName: string;
  providerID: string;
  mode: string;
  workDir: string;
  shellPath?: string;
}): Promise<string> {
  return callApi('[api.session.launchPiSession]', () => LaunchPiSession(
    params.modelName,
    params.providerID,
    params.mode,
    params.workDir,
    params.shellPath || ''
  ));
}

/**
 * Launch an Oh My Pi (omp) coding agent session
 */
export function launchOmpSession(params: {
  modelName: string;
  providerID: string;
  mode: string;
  workDir: string;
  shellPath?: string;
}): Promise<string> {
  return callApi('[api.session.launchOmpSession]', () => LaunchOmpSession(
    params.modelName,
    params.providerID,
    params.mode,
    params.workDir,
    params.shellPath || ''
  ));
}

/**
 * Stop a session
 */
export function stopSession(sessionId: string): Promise<void> {
  return callApi('[api.session.stopSession]', () => StopSession(sessionId));
}

/**
 * Get all sessions
 */
export function getSessions(): Promise<SessionInfo[]> {
  return callApi('[api.session.getSessions]', () => GetSessions());
}

/**
 * Remove a session
 */
export function removeSession(sessionId: string): Promise<void> {
  return callApi('[api.session.removeSession]', () => RemoveSession(sessionId));
}
