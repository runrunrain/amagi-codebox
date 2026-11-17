/**
 * Shared Market API（共用插件市场，~/.agents/marketplaces）
 * 包装 wailsjs App 门面（app_sharedmarket.go 的 8 个方法），全部走 callApi
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
  RemoveSharedMarketplace,
  DeploySharedMarketplacePlugin,
  OpenSharedMarketplaceFolder,
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
  /** true = 插件内容不在市场仓库内（git-subdir 外部引用），无法本地部署 */
  externalSource?: boolean;
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
