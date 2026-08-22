/**
 * GitAssist API
 * Encapsulates AI-assisted git commit/push operations
 * Directly wraps wailsjs/go/gitassist/Service
 */

import {
  RepoInfo,
  ListBranches,
  SwitchBranch,
  SummarizeDiff,
  CommitAll,
  CommitStaged,
  Push,
} from '../../wailsjs/go/gitassist/Service';

import { gitassist } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
export type RepoStatus = gitassist.RepoStatus;
export type BranchInfo = gitassist.BranchInfo;

/**
 * Get git repo status for a working directory
 */
export function getRepoInfo(workDir: string): Promise<RepoStatus> {
  return callApi('[api.gitassist.getRepoInfo]', () => RepoInfo(workDir));
}

/**
 * List local branches
 */
export function listBranches(workDir: string): Promise<BranchInfo[]> {
  return callApi('[api.gitassist.listBranches]', () => ListBranches(workDir));
}

/**
 * Switch to another local branch
 */
export function switchBranch(workDir: string, branch: string): Promise<void> {
  return callApi('[api.gitassist.switchBranch]', () => SwitchBranch(workDir, branch));
}

/**
 * Generate commit message via LLM（后端未配置模型时返回中文错误）
 */
export function summarizeDiff(workDir: string): Promise<string> {
  return callApi('[api.gitassist.summarizeDiff]', () => SummarizeDiff(workDir));
}

/**
 * Commit all changes (stage everything then commit)
 */
export function commitAll(workDir: string, message: string): Promise<void> {
  return callApi('[api.gitassist.commitAll]', () => CommitAll(workDir, message));
}

/**
 * Commit only staged changes
 */
export function commitStaged(workDir: string, message: string): Promise<void> {
  return callApi('[api.gitassist.commitStaged]', () => CommitStaged(workDir, message));
}

/**
 * Push to upstream, returns summary text
 */
export function push(workDir: string): Promise<string> {
  return callApi('[api.gitassist.push]', () => Push(workDir));
}
