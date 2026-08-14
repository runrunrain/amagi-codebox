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
  GetSession,
  RemoveSession,
  ClearStoppedSessions,
  PtyWrite,
  PtyWriteLarge,
  PtyResize,
  GetOutputHistorySnapshot,
  GetPtyDimensions,
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
  useHeadroom: boolean;
  shellPath?: string;
}): Promise<string> {
  return callApi('[api.session.launchClaudeSession]', () => LaunchSession(
    params.providerName,
    params.presetName,
    params.mode,
    params.workDir,
    params.useHeadroom,
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
 * Get a specific session
 */
export function getSession(sessionId: string): Promise<session.SessionInfo> {
  return callApi('[api.session.getSession]', () => GetSession(sessionId));
}

/**
 * Remove a session
 */
export function removeSession(sessionId: string): Promise<void> {
  return callApi('[api.session.removeSession]', () => RemoveSession(sessionId));
}

/**
 * Clear all stopped sessions
 */
export function clearStoppedSessions(): Promise<number> {
  return callApi('[api.session.clearStoppedSessions]', () => ClearStoppedSessions());
}

/**
 * Write to PTY
 */
export function ptyWrite(sessionId: string, data: string): Promise<void> {
  return callApi('[api.session.ptyWrite]', () => PtyWrite(sessionId, data));
}

/**
 * Write large data to PTY
 */
export function ptyWriteLarge(sessionId: string, data: string): Promise<void> {
  return callApi('[api.session.ptyWriteLarge]', () => PtyWriteLarge(sessionId, data));
}

/**
 * Resize PTY
 */
export function ptyResize(sessionId: string, cols: number, rows: number): Promise<void> {
  return callApi('[api.session.ptyResize]', () => PtyResize(sessionId, cols, rows));
}

/**
 * Get output history snapshot
 */
export function getOutputHistorySnapshot(sessionId: string): Promise<string> {
  return callApi('[api.session.getOutputHistorySnapshot]', () => GetOutputHistorySnapshot(sessionId));
}

/**
 * Get PTY dimensions.
 * Backend packs cols and rows into a single number (cols * 1000 + rows).
 */
export function getPtyDimensions(sessionId: string): Promise<{ cols: number; rows: number }> {
  return callApi('[api.session.getPtyDimensions]', async () => {
    const packed = await GetPtyDimensions(sessionId);
    return { cols: Math.floor(packed / 1000), rows: packed % 1000 };
  });
}
