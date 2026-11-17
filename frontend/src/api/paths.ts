/**
 * Paths API
 * Encapsulates file system path operations
 * Directly wraps wailsjs/go/paths/PathsService and main/App for browse/edit
 */

import {
  ListDirectories,
} from '../../wailsjs/go/paths/PathsService';

import { SessionListDirectories } from '../../wailsjs/go/main/App';
import { callApi } from './internal/call';

// Type alias

/**
 * ListDirectories 返回的单条子目录记录。
 */
export interface DirectoryEntry {
  name: string;
  path: string;
}

/**
 * ListDirectories 返回的 JSON 载荷（前端解析后形态）。
 * parent：上级目录绝对路径；文件系统根目录时为 null。
 */
export interface ListDirectoriesResult {
  root: string;
  parent: string | null;
  dirs: DirectoryEntry[];
  truncated: boolean;
}

/**
 * List directories one level under root (directories only, dot-prefixed
 * skipped, case-insensitive sort, capped at 500 with truncated flag).
 * Empty root falls back to the user home directory (backend behavior).
 * The backend returns a JSON string; parsed here before returning.
 */
export function listDirectories(root: string): Promise<ListDirectoriesResult> {
  return callApi('[api.paths.listDirectories]', async () => {
    const raw = await ListDirectories(root);
    return JSON.parse(raw) as ListDirectoriesResult;
  });
}

/**
 * 会话感知的目录列表（快抱功能「输入工作路径」）：后端以会话权威启动
 * 上下文判定目标文件系统 —— WSL 内嵌会话在目标 distro 内列出（空 root
 * = distro $HOME，Windows 盘符 root 实测转换为 Linux 路径），其余会话
 * 与 listDirectories 完全同路（原生行为不变）。载荷 JSON 同形。
 */
export function sessionListDirectories(
  sessionId: string,
  root: string,
): Promise<ListDirectoriesResult> {
  return callApi('[api.paths.sessionListDirectories]', async () => {
    const raw = await SessionListDirectories(sessionId, root);
    return JSON.parse(raw) as ListDirectoriesResult;
  });
}
