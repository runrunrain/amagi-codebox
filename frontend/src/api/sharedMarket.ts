/**
 * Shared Market API（共用插件市场，~/.agents/marketplaces）
 * 包装 wailsjs App 门面（app_sharedmarket.go），全部走 callApi
 * 统一 "log + rethrow" 语义（api/sharedSkills.ts 同范式）。
 *
 * 本地声明 interface 而非 import wailsjs models，避免手写漂移
 * （与 sharedSkills.ts 契约一致）。
 */

import {
  ListSharedMarkets,
  RefreshSharedMarkets,
  AddSharedMarketplace,
  ListClaudeMarketplacesForImport,
  ImportSharedMarketplacesFromClaude,
  UpdateSharedMarketplace,
  UpdateSharedMarketplacePlugin,
  RemoveSharedMarketplace,
  DeploySharedMarketplacePlugin,
  OpenSharedMarketplaceFolder,
  GetSharedSkillOrigin,
  ListSharedSkillOrigins,
  UninstallSharedMarketplacePlugin,
} from '../../wailsjs/go/main/App';

import { callApi } from './internal/call';

/** 市场目录里的一个插件条目（含可部署技能枚举） */
export interface SharedMarketPlugin {
  name: string;
  version?: string;
  description?: string;
  author?: string;
  /** 可部署的技能目录名（本地 source 存在的条目） */
  skills: string[];
  /** 当前仍存在且来源凭据（provenance 标记）指向本市场本插件的技能目录名；
   * 同名但无凭据的目录不计入（不按名称碰撞推断）。 */
  deployedSkills?: string[];
  /** true = 插件内容不在市场仓库内（git-subdir 外部引用），无法本地部署 */
  externalSource?: boolean;
  updatable: boolean;
}

export interface SharedMarketPluginUpdateReport {
  plugin: SharedMarketPlugin;
  updatedSkills: string[];
  retainedSkills: string[];
}

/** 一个共用市场的视图（List 附带完整目录扫描） */
export interface SharedMarketplace {
  name: string;
  source: string;
  clonePath: string;
  description: string;
  addedAt: string;
  lastUpdated: string;
  pluginCount: number;
  plugins: SharedMarketPlugin[];
  /** clone 目录或 marketplace.json 不可读（「更新」会尝试重新 clone 恢复） */
  missing?: boolean;
}

/** 一次插件部署的结果（部署到 ~/.agents/skills） */
export interface SharedMarketDeployReport {
  marketplace: string;
  plugin: string;
  /** 已部署的技能目录名 */
  deployed: string[];
  /** 目标已存在而跳过的技能目录名（不覆盖，防丢用户改动） */
  skipped: string[];
  targetRoot: string;
}

/** 「从 Claude Code 导入」选择器条目 */
export interface ClaudeMarketplaceForImport {
  name: string;
  /** 可 clone 源（转换后；转换失败为空串） */
  source: string;
  repo?: string;
  url?: string;
  /** 已存在于共用市场 */
  imported: boolean;
}

/** 单个共用技能目录的部署来源凭据（目录 + 受限隐藏标记组成证据） */
export interface SharedSkillOrigin {
  /** 技能目录名（与凭据内 skill 一致，已后端校验） */
  skill: string;
  /** 部署来源市场名 */
  marketplace: string;
  /** 部署来源插件名（catalog 透传标识） */
  plugin: string;
  /** 部署时间（RFC3339，凭据写入时刻；不代表技能内容版本） */
  deployedAt: string;
  /** 位于 skills/ → true；skills-disabled/ → false */
  enabled: boolean;
  /** 技能目录绝对路径 */
  path: string;
}

/** 市场插件卸载结果（仅移除来源凭据完全匹配的目录） */
export interface SharedMarketUninstallReport {
  marketplace: string;
  plugin: string;
  /** 已删除的技能目录名（含停用态目录）；同名手动/无凭据目录不在其列 */
  removed: string[];
}

/** 枚举共用市场（状态 + 实时目录扫描） */
export function listSharedMarkets(): Promise<SharedMarketplace[]> {
  return callApi('[api.sharedMarket.listSharedMarkets]', () => ListSharedMarkets());
}

/** 显式刷新（语义等同 list，契约点保留） */
export function refreshSharedMarkets(): Promise<SharedMarketplace[]> {
  return callApi('[api.sharedMarket.refreshSharedMarkets]', () => RefreshSharedMarkets());
}

/** 从 git 源（URL 或本地路径）新增共用市场，返回最新列表 */
export function addSharedMarketplace(source: string): Promise<SharedMarketplace[]> {
  return callApi('[api.sharedMarket.addSharedMarketplace]', () => AddSharedMarketplace(source));
}

/** 列出 Claude Code 已注册市场并标记是否已导入（导入选择器数据源） */
export function listClaudeMarketplacesForImport(): Promise<ClaudeMarketplaceForImport[]> {
  return callApi(
    '[api.sharedMarket.listClaudeMarketplacesForImport]',
    () => ListClaudeMarketplacesForImport(),
  );
}

/** 按名称从 Claude Code 注册表导入市场（保持同名），返回最新列表 */
export function importSharedMarketplacesFromClaude(names: string[]): Promise<SharedMarketplace[]> {
  return callApi(
    '[api.sharedMarket.importSharedMarketplacesFromClaude]',
    () => ImportSharedMarketplacesFromClaude(names),
  );
}

/** 拉取市场最新内容（缺失时重新 clone），返回最新列表 */
export function updateSharedMarketplace(name: string): Promise<SharedMarketplace[]> {
  return callApi('[api.sharedMarket.updateSharedMarketplace]', () => UpdateSharedMarketplace(name));
}

export function updateSharedMarketplacePlugin(marketName: string, pluginName: string): Promise<SharedMarketPluginUpdateReport> {
  return callApi('[api.sharedMarket.updateSharedMarketplacePlugin]', () =>
    UpdateSharedMarketplacePlugin(marketName, pluginName),
  );
}

/** 删除共用市场（clone 目录 + 状态；已部署技能不受影响），返回最新列表 */
export function removeSharedMarketplace(name: string): Promise<SharedMarketplace[]> {
  return callApi('[api.sharedMarket.removeSharedMarketplace]', () => RemoveSharedMarketplace(name));
}

/** 把市场内插件的技能部署到 ~/.agents/skills（成为共用技能） */
export function deploySharedMarketplacePlugin(
  marketName: string,
  pluginName: string,
): Promise<SharedMarketDeployReport> {
  return callApi(
    '[api.sharedMarket.deploySharedMarketplacePlugin]',
    () => DeploySharedMarketplacePlugin(marketName, pluginName),
  );
}

/** 打开市场 clone 目录（系统文件管理器） */
export function openSharedMarketplaceFolder(name: string): Promise<void> {
  return callApi('[api.sharedMarket.openSharedMarketplaceFolder]', () =>
    OpenSharedMarketplaceFolder(name),
  );
}

/**
 * 读取单个共用技能目录的部署来源凭据。
 * 目录存在但无有效凭据（手动条目或旧版部署）→ null；目录不存在 → reject。
 */
export function getSharedSkillOrigin(skillName: string): Promise<SharedSkillOrigin | null> {
  return callApi('[api.sharedMarket.getSharedSkillOrigin]', () =>
    GetSharedSkillOrigin(skillName),
  );
}

/** 枚举两个技能根下全部带有效来源凭据的共用技能（无凭据目录不列出） */
export function listSharedSkillOrigins(): Promise<SharedSkillOrigin[]> {
  return callApi('[api.sharedMarket.listSharedSkillOrigins]', () =>
    ListSharedSkillOrigins(),
  );
}

/**
 * 显式卸载市场插件的已部署共用技能：仅删除来源凭据完全匹配（市场 + 插件 +
 * 目录名）的启用/停用目录；同名手动条目与无凭据目录保留；市场 clone 不受影响。
 */
export function uninstallSharedMarketplacePlugin(
  marketName: string,
  pluginName: string,
): Promise<SharedMarketUninstallReport> {
  return callApi('[api.sharedMarket.uninstallSharedMarketplacePlugin]', () =>
    UninstallSharedMarketplacePlugin(marketName, pluginName),
  );
}
