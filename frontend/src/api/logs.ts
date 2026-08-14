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
import { callApi } from './internal/call';

type Entry = logging.Entry;

/**
 * Get logs with optional filters
 */
export function getLogs(params: {
  level?: string;
  source?: string;
  keyword?: string;
  limit?: number;
}): Promise<Entry[]> {
  return callApi('[api.logs.getLogs]', () => GetLogs(
    params.level || '',
    params.source || '',
    params.keyword || '',
    params.limit || 100
  ));
}

/**
 * Get log sources
 */
export function getLogSources(): Promise<string[]> {
  return callApi('[api.logs.getLogSources]', () => GetLogSources());
}

/**
 * Get log files
 */
export function getLogFiles(): Promise<string[]> {
  return callApi('[api.logs.getLogFiles]', () => GetLogFiles());
}

/**
 * Get log file content
 */
export function getLogFileContent(filename: string): Promise<string> {
  return callApi('[api.logs.getLogFileContent]', () => GetLogFileContent(filename));
}

/**
 * Clear logs
 */
export function clearLogs(): Promise<void> {
  return callApi('[api.logs.clearLogs]', () => ClearLogs());
}

/**
 * Export logs
 */
export function exportLogs(): Promise<string> {
  return callApi('[api.logs.exportLogs]', () => ExportLogs());
}
