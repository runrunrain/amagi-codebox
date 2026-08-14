/**
 * Headroom API
 * Encapsulates Headroom context-compression proxy operations.
 * M3-A2: raw HeadroomService removed from Bind; mutations go through App facade (C-01).
 */

import {
  HeadroomStart,
  HeadroomStop,
  HeadroomIsRunning,
  HeadroomGetStatus,
  HeadroomGetPort,
} from '../../wailsjs/go/main/App';
import { GetHeadroomSavings, GetHeadroomPerfByClient } from '../../wailsjs/go/main/App';

import { headroom } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

// Type aliases
type HeadroomStatus = headroom.HeadroomStatus;
type SavingsReport = headroom.SavingsReport;
type ClientPerfStat = headroom.ClientPerfStat;

/**
 * Start the Headroom proxy subprocess.
 * realBackendUrl is the real upstream API base URL; Headroom forwards
 * compressed traffic to it via ANTHROPIC_TARGET_API_URL.
 */
export function startHeadroom(backendUrl: string): Promise<void> {
  return callApi('[api.headroom.startHeadroom]', () => HeadroomStart(backendUrl));
}

/**
 * Stop the Headroom proxy subprocess. No-op if not running.
 */
export function stopHeadroom(): Promise<void> {
  return callApi('[api.headroom.stopHeadroom]', () => HeadroomStop());
}

/**
 * Check whether the Headroom proxy is currently running.
 */
export function isHeadroomRunning(): Promise<boolean> {
  return callApi('[api.headroom.isHeadroomRunning]', () => HeadroomIsRunning());
}

/**
 * Get the Headroom proxy status snapshot (running / port / backendUrl).
 */
export function getHeadroomStatus(): Promise<HeadroomStatus> {
  return callApi('[api.headroom.getHeadroomStatus]', () => HeadroomGetStatus());
}

/**
 * Get the port Headroom is configured to listen on.
 */
export function getHeadroomPort(): Promise<number> {
  return callApi('[api.headroom.getHeadroomPort]', () => HeadroomGetPort());
}

/**
 * Get the Headroom savings report (global cumulative ledger).
 * Reads the lifetime compression statistics persisted by the Headroom proxy.
 * Rejects when Headroom is not installed / not enabled / has no data file.
 */
export function getHeadroomSavings(): Promise<SavingsReport> {
  return callApi('[api.headroom.getHeadroomSavings]', () => GetHeadroomSavings());
}

/**
 * Get Headroom perf stats aggregated by client.
 *
 * Runs `headroom perf --format json --raw` and aggregates per-record data into
 * one stat per client: request count, average prefix-cache hit rate, cumulative
 * tokens_saved, cache_read_tokens, tokens_before and savings_percent.
 *
 * This is the honest data source for the codex card: codex traffic flowing
 * through headroom yields near-zero tokens_saved (headroom's compression of the
 * OpenAI responses protocol is still early), but a stable, high prefix-cache
 * hit rate — which is the real saving (cached tokens are billed at roughly 1/5
 * of fresh tokens). Claude traffic, by contrast, gets real body compression
 * (tool_schema_compaction etc.) so tokens_saved is its primary metric.
 *
 * Rejects when Headroom is not installed / perf subcommand fails / JSON parse
 * fails; never returns fabricated data.
 */
export function getHeadroomPerfByClient(): Promise<ClientPerfStat[]> {
  return callApi('[api.headroom.getHeadroomPerfByClient]', () => GetHeadroomPerfByClient());
}
