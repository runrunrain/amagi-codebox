/**
 * Session API
 * Encapsulates Wails session operations
 */

import {
  LaunchSession,
  LaunchOpenCode,
  LaunchCodexSession,
  StopSession,
  StopAllSessions,
  GetSessions,
  GetSession,
  RemoveSession,
  ClearStoppedSessions,
  PtyWrite,
  PtyWriteLarge,
  PtyResize,
  GetOutputHistorySnapshot,
  RegisterOutputCallback,
  UnregisterOutputCallback,
  RegisterExitCallback,
  UnregisterExitCallback,
  RegisterResizeCallback,
  UnregisterResizeCallback,
  GetPtyDimensions,
  AttachSessionObserver,
  DetachSessionObserver,
} from '../../wailsjs/go/main/App';

import { session } from '../../wailsjs/go/models';

// Type alias
type SessionInfo = session.SessionInfo;

/**
 * Launch a Claude Code session
 */
export async function launchClaudeSession(params: {
  providerName: string;
  presetName: string;
  mode: string;
  workDir: string;
  useProxy: boolean;
  shellPath?: string;
}): Promise<string> {
  try {
    return await LaunchSession(
      params.providerName,
      params.presetName,
      params.mode,
      params.workDir,
      params.useProxy,
      params.shellPath || ''
    );
  } catch (error) {
    console.error('Failed to launch Claude session:', error);
    throw error;
  }
}

/**
 * Launch an OpenCode session
 */
export async function launchOpenCodeSession(params: {
  providerName: string;
  presetName: string;
  mode: string;
  workDir: string;
  shellPath?: string;
}): Promise<string> {
  try {
    return await LaunchOpenCode(
      params.providerName,
      params.presetName,
      params.mode,
      params.workDir,
      params.shellPath || ''
    );
  } catch (error) {
    console.error('Failed to launch OpenCode session:', error);
    throw error;
  }
}

/**
 * Launch a Codex CLI session
 */
export async function launchCodexSession(params: {
  modelName: string;
  providerID: string;
  mode: string;
  workDir: string;
  shellPath?: string;
}): Promise<string> {
  try {
    return await LaunchCodexSession(
      params.modelName,
      params.providerID,
      params.mode,
      params.workDir,
      params.shellPath || ''
    );
  } catch (error) {
    console.error('Failed to launch Codex session:', error);
    throw error;
  }
}

/**
 * Stop a session
 */
export async function stopSession(sessionId: string): Promise<void> {
  try {
    await StopSession(sessionId);
  } catch (error) {
    console.error('Failed to stop session:', error);
    throw error;
  }
}

/**
 * Stop all sessions
 */
export async function stopAllSessions(): Promise<void> {
  try {
    await StopAllSessions();
  } catch (error) {
    console.error('Failed to stop all sessions:', error);
    throw error;
  }
}

/**
 * Get all sessions
 */
export async function getSessions(): Promise<SessionInfo[]> {
  try {
    return await GetSessions();
  } catch (error) {
    console.error('Failed to get sessions:', error);
    throw error;
  }
}

/**
 * Get a specific session
 */
export async function getSession(sessionId: string): Promise<session.SessionInfo> {
  try {
    return await GetSession(sessionId);
  } catch (error) {
    console.error('Failed to get session:', error);
    throw error;
  }
}

/**
 * Remove a session
 */
export async function removeSession(sessionId: string): Promise<void> {
  try {
    await RemoveSession(sessionId);
  } catch (error) {
    console.error('Failed to remove session:', error);
    throw error;
  }
}

/**
 * Clear all stopped sessions
 */
export async function clearStoppedSessions(): Promise<number> {
  try {
    return await ClearStoppedSessions();
  } catch (error) {
    console.error('Failed to clear stopped sessions:', error);
    throw error;
  }
}

/**
 * Write to PTY
 */
export async function ptyWrite(sessionId: string, data: string): Promise<void> {
  try {
    await PtyWrite(sessionId, data);
  } catch (error) {
    console.error('Failed to write to PTY:', error);
    throw error;
  }
}

/**
 * Write large data to PTY
 */
export async function ptyWriteLarge(sessionId: string, data: string): Promise<void> {
  try {
    await PtyWriteLarge(sessionId, data);
  } catch (error) {
    console.error('Failed to write large to PTY:', error);
    throw error;
  }
}

/**
 * Resize PTY
 */
export async function ptyResize(sessionId: string, cols: number, rows: number): Promise<void> {
  try {
    await PtyResize(sessionId, cols, rows);
  } catch (error) {
    console.error('Failed to resize PTY:', error);
    throw error;
  }
}

/**
 * Get output history snapshot
 */
export async function getOutputHistorySnapshot(sessionId: string): Promise<string> {
  try {
    return await GetOutputHistorySnapshot(sessionId);
  } catch (error) {
    console.error('Failed to get output history:', error);
    throw error;
  }
}

/**
 * Register output callback
 */
export function registerOutputCallback(sessionId: string, id: string, callback: (data: number[]) => void): void {
  RegisterOutputCallback(sessionId, id, callback);
}

/**
 * Unregister output callback
 */
export function unregisterOutputCallback(sessionId: string, id: string): void {
  UnregisterOutputCallback(sessionId, id);
}

/**
 * Register exit callback
 */
export function registerExitCallback(sessionId: string, id: string, callback: (exitCode: number) => void): void {
  RegisterExitCallback(sessionId, id, callback);
}

/**
 * Unregister exit callback
 */
export function unregisterExitCallback(sessionId: string, id: string): void {
  UnregisterExitCallback(sessionId, id);
}

/**
 * Register resize callback
 */
export function registerResizeCallback(sessionId: string, id: string, callback: (cols: number, rows: number) => void): void {
  RegisterResizeCallback(sessionId, id, callback);
}

/**
 * Unregister resize callback
 */
export function unregisterResizeCallback(sessionId: string, id: string): void {
  UnregisterResizeCallback(sessionId, id);
}

/**
 * Get PTY dimensions.
 * Backend packs cols and rows into a single number (cols * 1000 + rows).
 */
export async function getPtyDimensions(sessionId: string): Promise<{ cols: number; rows: number }> {
  try {
    const packed = await GetPtyDimensions(sessionId);
    return { cols: Math.floor(packed / 1000), rows: packed % 1000 };
  } catch (error) {
    console.error('Failed to get PTY dimensions:', error);
    throw error;
  }
}

/**
 * Attach session observer.
 * Returns the buffered output history (number[] of byte values).
 */
export async function attachSessionObserver(
  sessionId: string,
  id: string,
  outputCB: (data: number[]) => void,
  resizeCB: (cols: number, rows: number) => void
): Promise<number[]> {
  try {
    return await AttachSessionObserver(sessionId, id, outputCB, resizeCB);
  } catch (error) {
    console.error('Failed to attach session observer:', error);
    throw error;
  }
}

/**
 * Detach session observer
 */
export function detachSessionObserver(sessionId: string, id: string): void {
  DetachSessionObserver(sessionId, id);
}
