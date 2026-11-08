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
// 共享 DTO（S4 claude/codex 键级单元与技能/资源面消费）
// ---------------------------------------------------------------------------

export type WSLConfigUnitDetail = main.WSLConfigUnitDetail;
export type WSLConfigUnitPreview = main.WSLConfigUnitPreview;
export type WSLConfigUnitKeyPreview = main.WSLConfigUnitKeyPreview;
export type WSLConfigUnitOutcome = main.WSLConfigUnitOutcome;

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

// ---------------------------------------------------------------------------
// P3-S4：Claude / Codex 用户配置导入（键级选择导入 + 资源目录单元）与
// OpenCode 附加层 MCP 全局冲突覆盖选择。
// ---------------------------------------------------------------------------

import {
  GetWSLCliSupplyStatus,
  PreviewWSLCliConfigSupply,
  ImportWSLCliConfigKeys,
  ApplyWSLCliConfigSupply,
  KeepWSLCliConfigUnitLocal,
  TakeOverWSLCliConfigUnit,
  CheckWSLCliResourcesSupply,
  PreviewWSLCliResourcesSupply,
  ApplyWSLCliResourcesSupply,
  KeepWSLCliResourceLocal,
  TakeOverWSLCliResource,
  GetWSLOpenCodeMCPPreview,
  SetWSLOpenCodeMCPSelection,
} from '../../wailsjs/go/main/App';

export type WSLCliSupplyStatus = main.WSLCliSupplyStatus;
export type WSLOpenCodeMCPEntry = main.WSLOpenCodeMCPEntry;

/**
 * 一个 CLI（claude/codex）的 S4 导入面状态：键级单元状态 + 资源扫描状态
 *（读缓存；资源后台扫描单飞启动，不阻塞）。
 */
export function getWSLCliSupplyStatus(cli: string): Promise<WSLCliSupplyStatus> {
  return callApi('[api.wslSupply.getWSLCliSupplyStatus]', () => GetWSLCliSupplyStatus(cli));
}

/**
 * 键级单元「预览」：逐键动作 + 转换前→转换后对照 + 选择状态与接管 CAS 令牌。
 */
export function previewWSLCliConfigSupply(cli: string): Promise<WSLConfigUnitPreview[]> {
  return callApi('[api.wslSupply.previewWSLCliConfigSupply]', () => PreviewWSLCliConfigSupply(cli));
}

/**
 * 键级单元「导入所选」：保存选择集 → 授予 per-target 同意 → 立即安全更新。
 */
export function importWSLCliConfigKeys(cli: string, unit: string, keys: string[]): Promise<WSLConfigUnitOutcome[]> {
  return callApi('[api.wslSupply.importWSLCliConfigKeys]', () => ImportWSLCliConfigKeys(cli, unit, keys));
}

/**
 * 键级单元「应用安全更新」：按当前选择集运行（冻结/未托管/转换受阻保持现状）。
 */
export function applyWSLCliConfigSupply(cli: string): Promise<WSLConfigUnitOutcome[]> {
  return callApi('[api.wslSupply.applyWSLCliConfigSupply]', () => ApplyWSLCliConfigSupply(cli));
}

/**
 * 键级单元「保留本地（停跟随）」。
 */
export function keepWSLCliConfigUnitLocal(cli: string, unit: string): Promise<void> {
  return callApi('[api.wslSupply.keepWSLCliConfigUnitLocal]', () => KeepWSLCliConfigUnitLocal(cli, unit));
}

/**
 * 键级单元「确认接管（预览后）」：预览 hash CAS；旧预览过期则拒绝。
 */
export function takeOverWSLCliConfigUnit(cli: string, unit: string, expectedTargetHash: string): Promise<void> {
  return callApi('[api.wslSupply.takeOverWSLCliConfigUnit]', () => TakeOverWSLCliConfigUnit(cli, unit, expectedTargetHash));
}

/**
 * 资源面「检查」：触发后台重扫并返回当前状态。
 */
export function checkWSLCliResourcesSupply(cli: string): Promise<WSLSkillsSupplyStatus> {
  return callApi('[api.wslSupply.checkWSLCliResourcesSupply]', () => CheckWSLCliResourcesSupply(cli));
}

/**
 * 资源面「预览」：逐资源动作/不兼容原因/接管 CAS 令牌。
 */
export function previewWSLCliResourcesSupply(cli: string): Promise<WSLSkillPreview[]> {
  return callApi('[api.wslSupply.previewWSLCliResourcesSupply]', () => PreviewWSLCliResourcesSupply(cli));
}

/**
 * 资源「预览勾选后导入」：登记 per-target 同意并供给（S3 机制复用）。
 */
export function applyWSLCliResourcesSupply(cli: string, rels: string[]): Promise<WSLConfigUnitOutcome[]> {
  return callApi('[api.wslSupply.applyWSLCliResourcesSupply]', () => ApplyWSLCliResourcesSupply(cli, rels));
}

/**
 * 资源「保留本地（停跟随）」。
 */
export function keepWSLCliResourceLocal(cli: string, rel: string): Promise<void> {
  return callApi('[api.wslSupply.keepWSLCliResourceLocal]', () => KeepWSLCliResourceLocal(cli, rel));
}

/**
 * 资源「确认接管（预览后）」。
 */
export function takeOverWSLCliResource(cli: string, rel: string, expectedTargetHash: string): Promise<void> {
  return callApi('[api.wslSupply.takeOverWSLCliResource]', () => TakeOverWSLCliResource(cli, rel, expectedTargetHash));
}

/**
 * OpenCode 附加层 MCP 预览：全部 preset 的 mcp 条目分类（形态/全局同名冲突）
 * 与当前覆盖选择（只读，不写）。
 */
export function getWSLOpenCodeMCPPreview(): Promise<WSLOpenCodeMCPEntry[]> {
  return callApi('[api.wslSupply.getWSLOpenCodeMCPPreview]', () => GetWSLOpenCodeMCPPreview());
}

/**
 * 保存 OpenCode 附加层 MCP 的全局同名冲突覆盖选择（默认保留本地；勾选
 * 覆盖的 server 才写入附加层）。
 */
export function setWSLOpenCodeMCPSelection(overrideGlobal: string[]): Promise<void> {
  return callApi('[api.wslSupply.setWSLOpenCodeMCPSelection]', () => SetWSLOpenCodeMCPSelection(overrideGlobal));
}

// ---------------------------------------------------------------------------
// P2-B：统一配置管理（一次启用 → 启动/保存/对账自动同步）。
// ---------------------------------------------------------------------------

import { EnableWSLUnifiedSupply, DisableWSLUnifiedSupply } from '../../wailsjs/go/main/App';

/**
 * 「启用统一管理」：对当前 distro 一次性授权固定单元集（Pi settings/amagi/mcp、
 * 视觉清单、Claude MCP/设置白名单、Codex MCP/profile），此后会话启动、保存
 * 推送与启动对账自动同步，新条目随下次对账纳入（无需重复预览/勾选）。逐单元
 * 返回结果，不汇总成单一成功。
 */
export function enableWSLUnifiedSupply(): Promise<WSLConfigUnitOutcome[]> {
  return callApi('[api.wslSupply.enableWSLUnifiedSupply]', () => EnableWSLUnifiedSupply());
}

/**
 * 「停止自动纳入新项」：停止统一管理的自动同步；已授权单元与既有副本保持
 * 现状（撤回单个单元用既有「保留本地」）。distro 重装后需重新启用。
 */
export function disableWSLUnifiedSupply(): Promise<void> {
  return callApi('[api.wslSupply.disableWSLUnifiedSupply]', () => DisableWSLUnifiedSupply());
}
