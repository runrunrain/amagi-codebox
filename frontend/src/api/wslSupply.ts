/**
 * WSL Config Supply API (P3-S1)
 * Minimal status surface for the「配置状态」section under the WSL CLI card:
 * per-face (providers / vision export / modality KB / OpenCode layer) sync
 * state and the per-target vision-export consent entry (key cross-boundary
 * authorization). Wraps the App-bound methods GetWSLConfigSupplyStatus /
 * GrantWSLVisionConsent (generated bindings, never hand-edited).
 */

import { GetWSLConfigSupplyStatus, GrantWSLVisionConsent } from '../../wailsjs/go/main/App';
import { main } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type WSLConfigSupplyStatus = main.WSLConfigSupplyStatus;
export type WSLConfigSupplyFace = main.WSLConfigSupplyFace;

/**
 * Read the sanitized WSL config-supply status snapshot (cache read only: never
 * triggers probing, installation or sync).
 */
export function getWSLConfigSupplyStatus(): Promise<WSLConfigSupplyStatus> {
  return callApi('[api.wslSupply.getWSLConfigSupplyStatus]', () => GetWSLConfigSupplyStatus());
}

/**
 * Grant the per-target consent to share the vision export (key copy) into the
 * current WSL distro. Idempotent; schedules one async push afterwards.
 */
export function grantWSLVisionConsent(): Promise<void> {
  return callApi('[api.wslSupply.grantWSLVisionConsent]', () => GrantWSLVisionConsent());
}

// ---------------------------------------------------------------------------
// P3-S2：Pi 配置键级投影（settings/amagi/mcp）的动作面（D4 动作分离：检查 /
// 预览同步 / 应用安全更新 / 保留本地 / 确认接管——没有强制覆盖全部）。
// ---------------------------------------------------------------------------

import {
  CheckWSLPiConfigSupply,
  PreviewWSLPiConfigSupply,
  ApplyWSLPiConfigSupply,
  KeepWSLPiConfigUnitLocal,
  TakeOverWSLPiConfigUnit,
} from '../../wailsjs/go/main/App';

export type WSLConfigUnitDetail = main.WSLConfigUnitDetail;
export type WSLConfigUnitPreview = main.WSLConfigUnitPreview;
export type WSLConfigUnitKeyPreview = main.WSLConfigUnitKeyPreview;
export type WSLConfigUnitOutcome = main.WSLConfigUnitOutcome;

/**
 * 「检查」：只读探测当前目标并刷新状态快照（可唤醒当前目标，不写配置）。
 */
export function checkWSLPiConfigSupply(): Promise<WSLConfigSupplyStatus> {
  return callApi('[api.wslSupply.checkWSLPiConfigSupply]', () => CheckWSLPiConfigSupply());
}

/**
 * 「预览同步」：逐单元/逐键动作 + 接管 CAS 令牌（targetHash 不属展示内容，
 * 仅回传给「确认接管」）。
 */
export function previewWSLPiConfigSupply(): Promise<WSLConfigUnitPreview[]> {
  return callApi('[api.wslSupply.previewWSLPiConfigSupply]', () => PreviewWSLPiConfigSupply());
}

/**
 * 「应用安全更新」：只更新未冻结键；冻结/未托管/转换受阻键保持 WSL 现状。
 */
export function applyWSLPiConfigSupply(): Promise<WSLConfigUnitOutcome[]> {
  return callApi('[api.wslSupply.applyWSLPiConfigSupply]', () => ApplyWSLPiConfigSupply());
}

/**
 * 「保留本地（停跟随）」：目标内容不动，后续供给零动作；显式接管可解除。
 */
export function keepWSLPiConfigUnitLocal(unit: string): Promise<void> {
  return callApi('[api.wslSupply.keepWSLPiConfigUnitLocal]', () => KeepWSLPiConfigUnitLocal(unit));
}

/**
 * 「确认接管（预览后）」：以预览目标 hash 做 CAS，用当前来源覆写托管域；
 * 旧预览过期则拒绝（旧预览不覆盖新编辑）。
 */
export function takeOverWSLPiConfigUnit(unit: string, expectedTargetHash: string): Promise<void> {
  return callApi('[api.wslSupply.takeOverWSLPiConfigUnit]', () => TakeOverWSLPiConfigUnit(unit, expectedTargetHash));
}

import { EnableWSLPiConfigSupply } from '../../wailsjs/go/main/App';

/**
 * 「启用投影」（F-3 同意门）：授予当前 distro 的 settings/amagi/mcp 投影
 * 同意（Windows 用户配置跨界是授权动作）；授权后未手改条目跟随 Windows
 * 面板。distro 重装后需重新同意。
 */
export function enableWSLPiConfigSupply(): Promise<void> {
  return callApi('[api.wslSupply.enableWSLPiConfigSupply]', () => EnableWSLPiConfigSupply());
}

// ---------------------------------------------------------------------------
// P3-S3：共享技能（Windows ~/.agents/skills → WSL）——有界扫描、预览勾选
// 导入（per-target 同意）、增量跟随与冻结保留（TrackedProjection）。
// ---------------------------------------------------------------------------

import {
  CheckWSLSkillsSupply,
  GetWSLSkillsSupplyStatus,
  PreviewWSLSkillsSupply,
  ApplyWSLSkillsSupply,
  KeepWSLSkillLocal,
  TakeOverWSLSkill,
} from '../../wailsjs/go/main/App';

export type WSLSkillsSupplyStatus = main.WSLSkillsSupplyStatus;
export type WSLSkillSummary = main.WSLSkillSummary;
export type WSLSkillPreview = main.WSLSkillPreview;

/**
 * 技能面状态：单飞启动后台扫描（分批推进），立即返回缓存快照——大目录
 * 不阻塞 UI；扫描未完成的技能明确标记，不冒充完成。
 */
export function getWSLSkillsSupplyStatus(): Promise<WSLSkillsSupplyStatus> {
  return callApi('[api.wslSupply.getWSLSkillsSupplyStatus]', () => GetWSLSkillsSupplyStatus());
}

/**
 * 「检查」：触发一轮后台重扫并返回当前状态（不写任何目标内容）。
 */
export function checkWSLSkillsSupply(): Promise<WSLSkillsSupplyStatus> {
  return callApi('[api.wslSupply.checkWSLSkillsSupply]', () => CheckWSLSkillsSupply());
}

/**
 * 「预览」：逐技能动作/不兼容原因（逐项报告）/接管 CAS 令牌；不产生写入。
 */
export function previewWSLSkillsSupply(): Promise<WSLSkillPreview[]> {
  return callApi('[api.wslSupply.previewWSLSkillsSupply]', () => PreviewWSLSkillsSupply());
}

/**
 * 「预览勾选后导入」：对勾选的可移植技能登记 per-target 同意并供给
 *（文件 0644/目录 0755；越界/绝对/循环链接与 Windows 专用脚本不复制）。
 */
export function applyWSLSkillsSupply(names: string[]): Promise<WSLConfigUnitOutcome[]> {
  return callApi('[api.wslSupply.applyWSLSkillsSupply]', () => ApplyWSLSkillsSupply(names));
}

/**
 * 「保留本地（停跟随）」：目标内容不动，后续供给零动作；接管可解除。
 */
export function keepWSLSkillLocal(name: string): Promise<void> {
  return callApi('[api.wslSupply.keepWSLSkillLocal]', () => KeepWSLSkillLocal(name));
}

/**
 * 「确认接管（预览后）」：目标树 hash 做 CAS；旧预览过期则拒绝。
 */
export function takeOverWSLSkill(name: string, expectedTargetHash: string): Promise<void> {
  return callApi('[api.wslSupply.takeOverWSLSkill]', () => TakeOverWSLSkill(name, expectedTargetHash));
}
