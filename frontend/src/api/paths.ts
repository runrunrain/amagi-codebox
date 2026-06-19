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
} from '../../wailsjs/go/paths/PathsService';

import { BrowseDirectory, OpenFileInEditor } from '../../wailsjs/go/main/App';

import { paths } from '../../wailsjs/go/models';

// Type alias
type PathEntry = paths.PathEntry;

/**
 * Get paths
 */
export async function getPaths(): Promise<PathEntry[]> {
  try {
    return await GetPaths();
  } catch (error) {
    console.error('[api.paths.getPaths]', error);
    throw error;
  }
}

/**
 * Add path
 */
export async function addPath(entry: PathEntry): Promise<void> {
  try {
    await AddPath(entry);
  } catch (error) {
    console.error('[api.paths.addPath]', error);
    throw error;
  }
}

/**
 * Remove path
 */
export async function removePath(path: string): Promise<void> {
  try {
    await RemovePath(path);
  } catch (error) {
    console.error('[api.paths.removePath]', error);
    throw error;
  }
}

/**
 * Get default path
 */
export async function getDefaultPath(): Promise<string> {
  try {
    return await GetDefaultPath();
  } catch (error) {
    console.error('[api.paths.getDefaultPath]', error);
    throw error;
  }
}

/**
 * Set default path
 */
export async function setDefaultPath(path: string): Promise<void> {
  try {
    await SetDefaultPath(path);
  } catch (error) {
    console.error('[api.paths.setDefaultPath]', error);
    throw error;
  }
}

/**
 * Update path label
 */
export async function updatePathLabel(path: string, label: string): Promise<void> {
  try {
    await UpdateLabel(path, label);
  } catch (error) {
    console.error('[api.paths.updatePathLabel]', error);
    throw error;
  }
}

/**
 * Validate path
 */
export async function validatePath(path: string): Promise<boolean> {
  try {
    return await ValidatePath(path);
  } catch (error) {
    console.error('[api.paths.validatePath]', error);
    throw error;
  }
}

/**
 * Load paths
 */
export async function loadPaths(): Promise<void> {
  try {
    await Load();
  } catch (error) {
    console.error('[api.paths.loadPaths]', error);
    throw error;
  }
}

/**
 * Save paths
 */
export async function savePaths(): Promise<void> {
  try {
    await Save();
  } catch (error) {
    console.error('[api.paths.savePaths]', error);
    throw error;
  }
}

/**
 * Browse directory (native file picker)
 */
export async function browseDirectory(): Promise<string> {
  try {
    return await BrowseDirectory();
  } catch (error) {
    console.error('[api.paths.browseDirectory]', error);
    throw error;
  }
}

/**
 * Open file in editor
 */
export async function openFileInEditor(filePath: string, line?: number): Promise<void> {
  try {
    await OpenFileInEditor(filePath, line || 0);
  } catch (error) {
    console.error('[api.paths.openFileInEditor]', error);
    throw error;
  }
}
