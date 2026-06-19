/**
 * Logs API
 * Encapsulates logging operations
 */

import {
  GetLogs,
  GetLogSources,
  GetLogFiles,
  GetLogFileContent,
  ClearLogs,
  ExportLogs,
} from '../../wailsjs/go/main/App';

import { logging } from '../../wailsjs/go/models';

type Entry = logging.Entry;

/**
 * Get logs with optional filters
 */
export async function getLogs(params: {
  level?: string;
  source?: string;
  keyword?: string;
  limit?: number;
}): Promise<Entry[]> {
  try {
    return await GetLogs(
      params.level || '',
      params.source || '',
      params.keyword || '',
      params.limit || 100
    );
  } catch (error) {
    console.error('Failed to get logs:', error);
    throw error;
  }
}

/**
 * Get log sources
 */
export async function getLogSources(): Promise<string[]> {
  try {
    return await GetLogSources();
  } catch (error) {
    console.error('Failed to get log sources:', error);
    throw error;
  }
}

/**
 * Get log files
 */
export async function getLogFiles(): Promise<string[]> {
  try {
    return await GetLogFiles();
  } catch (error) {
    console.error('Failed to get log files:', error);
    throw error;
  }
}

/**
 * Get log file content
 */
export async function getLogFileContent(filename: string): Promise<string> {
  try {
    return await GetLogFileContent(filename);
  } catch (error) {
    console.error('Failed to get log file content:', error);
    throw error;
  }
}

/**
 * Clear logs
 */
export async function clearLogs(): Promise<void> {
  try {
    await ClearLogs();
  } catch (error) {
    console.error('Failed to clear logs:', error);
    throw error;
  }
}

/**
 * Export logs
 */
export async function exportLogs(): Promise<string> {
  try {
    return await ExportLogs();
  } catch (error) {
    console.error('Failed to export logs:', error);
    throw error;
  }
}
