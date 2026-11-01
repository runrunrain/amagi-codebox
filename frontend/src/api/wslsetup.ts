/**
 * WSL CLI Setup API
 * Installs/upgrades the managed AI CLIs (Claude Code, OpenCode, Codex, Pi, OMP) INTO a WSL
 * distro so they run natively in the Linux environment CodeBox defaults to on
 * Windows. Wraps the App-bound methods GetWSLCLIStatus / InstallCLIToWSL.
 */

import { GetWSLCLIStatus, InstallCLIToWSL, UpdateCLIInWSL } from '../../wailsjs/go/main/App';
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
 * Install a CLI (claude / opencode / codex / pi / omp) into the WSL distro. Idempotent:
 * an already-installed CLI returns success with alreadyOK=true.
 */
export function installCLIToWSL(tool: string): Promise<WSLInstallResult> {
  return callApi('[api.wslsetup.installCLIToWSL]', () => InstallCLIToWSL(tool));
}

/** Upgrade the native Linux CLI, installing it if it is not present. */
export function updateCLIInWSL(tool: string): Promise<WSLInstallResult> {
  return callApi('[api.wslsetup.updateCLIInWSL]', () => UpdateCLIInWSL(tool));
}