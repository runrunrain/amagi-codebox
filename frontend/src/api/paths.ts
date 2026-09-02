/**
 * Paths API
 * Encapsulates file system path operations
 * Directly wraps wailsjs/go/paths/PathsService and main/App for browse/edit
 */

import {
  GetPaths,
  AddPath,
  RemovePath,
  GetDefaultPath,
  SetDefaultPath,
  UpdateLabel,
  ValidatePath,
  Load,
  Save,
  ListDirectories,
} from '../../wailsjs/go/paths/PathsService';

import { BrowseDirectory, OpenFileInEditor } from '../../wailsjs/go/main/App';

import { paths } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type alias
type PathEntry = paths.PathEntry;

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
 * Get paths
 */
export function getPaths(): Promise<PathEntry[]> {
  return callApi('[api.paths.getPaths]', () => GetPaths());
}

/**
 * Add path
 */
export function addPath(entry: PathEntry): Promise<void> {
  return callApi('[api.paths.addPath]', () => AddPath(entry));
}

/**
 * Remove path
 */
export function removePath(path: string): Promise<void> {
  return callApi('[api.paths.removePath]', () => RemovePath(path));
}

/**
 * Get default path
 */
export function getDefaultPath(): Promise<string> {
  return callApi('[api.paths.getDefaultPath]', () => GetDefaultPath());
}

/**
 * Set default path
 */
export function setDefaultPath(path: string): Promise<void> {
  return callApi('[api.paths.setDefaultPath]', () => SetDefaultPath(path));
}

/**
 * Update path label
 */
export function updatePathLabel(path: string, label: string): Promise<void> {
  return callApi('[api.paths.updatePathLabel]', () => UpdateLabel(path, label));
}

/**
 * Validate path
 */
export function validatePath(path: string): Promise<boolean> {
  return callApi('[api.paths.validatePath]', () => ValidatePath(path));
}

/**
 * Load paths
 */
export function loadPaths(): Promise<void> {
  return callApi('[api.paths.loadPaths]', () => Load());
}

/**
 * Save paths
 */
export function savePaths(): Promise<void> {
  return callApi('[api.paths.savePaths]', () => Save());
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
 * Browse directory (native file picker)
 */
export function browseDirectory(): Promise<string> {
  return callApi('[api.paths.browseDirectory]', () => BrowseDirectory());
}

/**
 * Open file in editor
 */
export function openFileInEditor(filePath: string, line?: number): Promise<void> {
  return callApi('[api.paths.openFileInEditor]', () => OpenFileInEditor(filePath, line || 0));
}
