/**
 * Headroom API
 * Encapsulates Headroom context-compression proxy operations.
 * Directly wraps wailsjs/go/headroom/HeadroomService.
 */

import {
  Start,
  Stop,
  IsRunning,
  GetStatus,
  GetPort,
} from '../../wailsjs/go/headroom/HeadroomService';
import { GetHeadroomSavings, GetHeadroomPerfByClient } from '../../wailsjs/go/main/App';

import { headroom } from '../../wailsjs/go/models';

// Type aliases
type HeadroomStatus = headroom.HeadroomStatus;
type SavingsReport = headroom.SavingsReport;
type ClientPerfStat = headroom.ClientPerfStat;

/**
 * Start the Headroom proxy subprocess.
 * realBackendUrl is the real upstream API base URL; Headroom forwards
 * compressed traffic to it via ANTHROPIC_TARGET_API_URL.
 */
export async function startHeadroom(backendUrl: string): Promise<void> {
  try {
    await Start(backendUrl);
  } catch (error) {
    console.error('[api.headroom.startHeadroom]', error);
    throw error;
  }
}

/**
 * Stop the Headroom proxy subprocess. No-op if not running.
 */
export async function stopHeadroom(): Promise<void> {
  try {
    await Stop();
  } catch (error) {
    console.error('[api.headroom.stopHeadroom]', error);
    throw error;
  }
}

/**
 * Check whether the Headroom proxy is currently running.
 */
export async function isHeadroomRunning(): Promise<boolean> {
  try {
    return await IsRunning();
  } catch (error) {
    console.error('[api.headroom.isHeadroomRunning]', error);
    throw error;
  }
}

/**
 * Get the Headroom proxy status snapshot (running / port / backendUrl).
 */
export async function getHeadroomStatus(): Promise<HeadroomStatus> {
  try {
    return await GetStatus();
  } catch (error) {
    console.error('[api.headroom.getHeadroomStatus]', error);
    throw error;
  }
}

/**
 * Get the port Headroom is configured to listen on.
 */
export async function getHeadroomPort(): Promise<number> {
  try {
    return await GetPort();
  } catch (error) {
    console.error('[api.headroom.getHeadroomPort]', error);
    throw error;
  }
}

/**
 * Get the Headroom savings report (global cumulative ledger).
 * Reads the lifetime compression statistics persisted by the Headroom proxy.
 * Rejects when Headroom is not installed / not enabled / has no data file.
 */
export async function getHeadroomSavings(): Promise<SavingsReport> {
  try {
    return await GetHeadroomSavings();
  } catch (error) {
    console.error('[api.headroom.getHeadroomSavings]', error);
    throw error;
  }
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
export async function getHeadroomPerfByClient(): Promise<ClientPerfStat[]> {
  try {
    return await GetHeadroomPerfByClient();
  } catch (error) {
    console.error('[api.headroom.getHeadroomPerfByClient]', error);
    throw error;
  }
}
