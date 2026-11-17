/**
 * Shared Extensions API（扩展管理页「共用」中心）
 *
 * 包装 internal/sharedext 的两个绑定面（与 piPlugin.ts 的双面模式一致）：
 *  - 简单 CRUD / 纯预览解析 → wailsjs/go/sharedext/Service（服务本体绑定，
 *    与其余 20 个已绑定服务同风格）；
 *  - 需要 App 上下文的入口 → wailsjs/go/main/App：选择对话框（需 Wails ctx）、
 *    聚合视图（五引擎 provider 组装）、skill/MCP 导入信封、缓存失效挂点。
 *
 * 注意：**不要**使用 sharedext/Service 的 ImportSkillFromZip/ImportSkillFromDir
 * 与 ImportMCPServers——服务层是多返回值（含部分成功信息），Wails v2 生成器
 * 只保留第一个返回值（部分明细被静默丢弃）；导入统一走 App 级
 * ImportSharedSkillFromZip/Dir 与 ImportSharedMCPServers 的单值信封。
 */

import {
  DeleteSharedMCPServer,
  DeleteSharedSkill,
  ListSharedMCPServers,
  ListSharedSkills,
  SetSharedMCPServerEnabled,
  SetSharedMCPServerTargets,
  SetSharedSkillEnabled,
  SetSharedSkillTargets,
} from '../../wailsjs/go/sharedext/Service';
import {
  GetAllSharedSkillsAggregated,
  ImportSharedMCPServers,
  ImportSharedSkillFromDir,
  ImportSharedSkillFromZip,
  InvalidateSharedExtCache,
  PickMCPJSONFile,
  PickSharedSkillDir,
  PickSharedSkillZipFile,
  PreviewSharedMCPJSON,
  ReadTextFile,
} from '../../wailsjs/go/main/App';
import { main, sharedext } from '../../wailsjs/go/models';

export type SharedSkill = sharedext.SharedSkill;
export type SharedMCPServer = sharedext.SharedMCPServer;
export type AggregatedSkill = sharedext.AggregatedSkill;
/** MCP 批量导入结果信封（App 级单值返回；skipped 非空 = 部分成功）。 */
export type ImportSharedMCPResult = main.ImportSharedMCPResult;
/** skill 导入结果信封（App 级单值返回；skipped 非空 = 部分成功，M3）。 */
export type ImportSharedSkillResult = main.ImportSharedSkillResult;

/**
 * 目标引擎标识（后端 sharedext.Engine 的取值域；wire 形态为 string，
 * 生成器未为具名字符串类型产出 TS 导出，故在此本地定义）。
 */
export type SharedExtEngine = 'claude' | 'codex' | 'opencode' | 'pi' | 'omp';

/** 全部受支持引擎（固定顺序，目标勾选 UI 用）。 */
export const SHARED_EXT_ENGINES: SharedExtEngine[] = ['claude', 'codex', 'opencode', 'pi', 'omp'];

// ---------------------------------------------------------------------------
// 公共 skill（简单 CRUD → 服务绑定）
// ---------------------------------------------------------------------------

/** 公共 skill 仓库清单（Missing=true 为仓库目录失联条目，可走删除清理）。 */
export async function getSharedSkills(): Promise<SharedSkill[]> {
  return (await ListSharedSkills()) ?? [];
}

/**
 * 从 zip 压缩包导入（可含多个 skill）。App 级单值信封（M3）：部分成功
 *（同名跳过）不报错，明细在 result.skipped；仅整体失败才 reject。
 */
export async function importSkillFromZip(path: string): Promise<ImportSharedSkillResult> {
  const result = await ImportSharedSkillFromZip(path);
  return result ?? { imported: [], skipped: [] };
}

/**
 * 从文件夹导入（自身含 SKILL.md 视为单个，否则扫描一层子目录）。
 * 部分成功语义同 importSkillFromZip（信封返回）。
 */
export async function importSkillFromDir(path: string): Promise<ImportSharedSkillResult> {
  const result = await ImportSharedSkillFromDir(path);
  return result ?? { imported: [], skipped: [] };
}

/** 删除公共 skill（先按 Deployed 回收各目标引擎，再删仓库与 registry 条目）。 */
export async function deleteSkill(name: string): Promise<void> {
  await DeleteSharedSkill(name);
}

/** 启用/禁用：true→分发到全部 Targets；false→回收全部 Deployed（幂等）。 */
export async function setSkillEnabled(name: string, enabled: boolean): Promise<void> {
  await SetSharedSkillEnabled(name, enabled);
}

/** 设置目标引擎（diff 分发/回收；Enabled=false 时只存不分发）。 */
export async function setSkillTargets(name: string, targets: SharedExtEngine[]): Promise<void> {
  await SetSharedSkillTargets(name, targets);
}

// ---------------------------------------------------------------------------
// 公共 MCP（读/预览/CRUD；导入走 App 信封）
// ---------------------------------------------------------------------------

/** 公共 MCP server 清单。 */
export async function getSharedMCPServers(): Promise<SharedMCPServer[]> {
  return (await ListSharedMCPServers()) ?? [];
}

/**
 * 纯解析预览 MCP JSON（不落库）。「粘贴/选文件 → 预览 → 勾选 →
 * importSharedMCPServers」两步交互的第一步；文本非法时抛后端中文报错。
 */
export async function previewMCPJSON(text: string): Promise<SharedMCPServer[]> {
  return (await PreviewSharedMCPJSON(text)) ?? [];
}

/**
 * 批量落库公共 MCP server。同名/非法条目进 result.skipped（部分成功不报错），
 * 仅 registry 写入失败才 reject。
 */
export async function importSharedMCPServers(servers: SharedMCPServer[]): Promise<ImportSharedMCPResult> {
  const result = await ImportSharedMCPServers(servers);
  return result ?? { imported: [], skipped: [] };
}

/** 删除公共 MCP server（先回收各目标引擎写入点）。 */
export async function deleteMCPServer(name: string): Promise<void> {
  await DeleteSharedMCPServer(name);
}

/** 启用/禁用（启停同步分发/回收各目标引擎的 MCP 配置写入点）。 */
export async function setMCPServerEnabled(name: string, enabled: boolean): Promise<void> {
  await SetSharedMCPServerEnabled(name, enabled);
}

/** 设置目标引擎。 */
export async function setMCPServerTargets(name: string, targets: SharedExtEngine[]): Promise<void> {
  await SetSharedMCPServerTargets(name, targets);
}

// ---------------------------------------------------------------------------
// 聚合视图（App 级：五引擎 provider 组装）
// ---------------------------------------------------------------------------

/**
 * 聚合 skill 视图（仓库手动条目 + 插件来源动态合并）。
 * force=true 旁路 30s TTL 缓存（前端刷新按钮传 true）。
 */
export async function getAggregatedSkills(force = false): Promise<AggregatedSkill[]> {
  return (await GetAllSharedSkillsAggregated(force)) ?? [];
}

/**
 * 使聚合缓存失效（下一次聚合强制重建）。P4 在插件更新/卸载后调用；
 * 手动 skill/MCP 变更无需调用（registry 写路径内建失效）。
 */
export async function invalidateSharedExtCache(): Promise<void> {
  await InvalidateSharedExtCache();
}

// ---------------------------------------------------------------------------
// 文件选择 / 读取（App 级：需要 Wails ctx）
// ---------------------------------------------------------------------------

/** 弹系统对话框选 skill zip；用户取消返回空串。 */
export async function pickSkillZip(): Promise<string> {
  return await PickSharedSkillZipFile();
}

/** 弹系统对话框选 skill 文件夹；用户取消返回空串。 */
export async function pickSkillDir(): Promise<string> {
  return await PickSharedSkillDir();
}

/** 弹系统对话框选 MCP JSON 文件；用户取消返回空串。 */
export async function pickMCPJsonFile(): Promise<string> {
  return await PickMCPJSONFile();
}

/** 读取所选 JSON 文件文本（1MB 上限）供前端预览；配合 pickMCPJsonFile 使用。 */
export async function readTextFile(path: string): Promise<string> {
  return await ReadTextFile(path);
}
