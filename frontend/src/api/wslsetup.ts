/**
 * WSL CLI Setup API
 * Installs the managed AI CLIs (Claude Code, OpenCode, Codex, Pi) INTO a WSL
 * distro so they run natively in the Linux environment CodeBox defaults to on
 * Windows. Wraps the App-bound methods GetWSLCLIStatus / InstallCLIToWSL.
 */

import { GetWSLCLIStatus, InstallCLIToWSL } from '../../wailsjs/go/main/App';
import { wslsetup } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type WSLStatus = wslsetup.Status;
export type WSLInstallResult = wslsetup.InstallResult;

/**
 * Get the WSL CLI environment status: whether a usable distro exists, the native
 * Node version, and which managed CLIs are installed natively inside WSL.
 */
export function getWSLCLIStatus(): Promise<WSLStatus> {
  return callApi('[api.wslsetup.getWSLCLIStatus]', () => GetWSLCLIStatus());
}

/**
 * Install a CLI (claude / opencode / codex / pi) into the WSL distro. Idempotent:
 * an already-installed CLI returns success with alreadyOK=true.
 */
export function installCLIToWSL(tool: string): Promise<WSLInstallResult> {
  return callApi('[api.wslsetup.installCLIToWSL]', () => InstallCLIToWSL(tool));
}