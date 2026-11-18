/**
 * Shared MCP API（共用 MCP，契约 §4.3）
 * 包装再生后的 wailsjs App 门面（app_sharedmcp.go 的 6 个方法），
 * 全部走 callApi 统一 "log + rethrow" 语义（api/internal/call.ts 既有范式，
 * sharedSkills.ts 同模式）。返回值均为最新 Overview（门面契约：前端免二次请求）。
 */

import {
  ListSharedMcp,
  ImportPickedSharedMcpFile,
  ImportSharedMcpEntries,
  ParseSharedMcpImportText,
  PickSharedMcpImportFile,
  CreateSharedMcpEntry,
  UpdateSharedMcpEntry,
  DeleteSharedMcpEntry,
  SetSharedMcpEngineSelection,
  OpenFileInEditor,
} from '../../wailsjs/go/main/App';

import { callApi } from './internal/call';

/**
 * 共用 MCP 登记条目（~/.agents/.mcp.json 的 mcpServers.<name> 应用视图）。
 * 字段对齐 App 门面请求/响应 sharedmcp.SharedMcpEntry（internal/sharedmcp
 * registry.go）；本地声明 interface 而非 import wailsjs models，
 * 避免手写漂移（sharedSkills.ts 先例）。
 */
export interface SharedMcpEntry {
  /** 条目名（标识；分发字符集 ^[A-Za-z0-9_-]+$，不合规名字只读警示） */
  name: string;
  /** "stdio"（本地命令）| "http"（远程）；其它值为登记文件既有未知形态（只读展示） */
  type: string;
  /** stdio 必填：可执行命令 */
  command?: string;
  /** stdio：命令参数 */
  args?: string[];
  /** stdio：环境变量（明文存于登记文件，UI 一律脱敏展示） */
  env?: Record<string, string>;
  /** http 必填：服务器 URL */
  url?: string;
  /** http：鉴权头等（值同 env，UI 脱敏展示） */
  headers?: Record<string, string>;
  description?: string;
  /** 信息性字段：登记文件中该条目所含未知键名清单（值不进 DTO，写回时原样合并） */
  unknownKeys?: string[];
}

/**
 * 分发矩阵一格（sharedmcp.EngineCell，writers.go）。
 * status 优先级 = 显示优先级：unsupported > conflict > drift > distributed > not-distributed；
 * "excluded" 仅 omp 行。
 */
export interface SharedMcpEngineCell {
  status: string;
  /** 用户勾选意图（state.Selected；与 Applied 同置/同清） */
  selected: boolean;
  reason?: string;
}

/** 单个登记条目 × 各引擎的状态矩阵（sharedmcp.EntryOverview） */
export interface SharedMcpEntryOverview {
  entry: SharedMcpEntry;
  /** 条目名是否符合分发字符集（false → 行级「名字不兼容分发」警示） */
  nameDistributable: boolean;
  /** key=引擎 ID（claude/codex/opencode/pi/omp） */
  engines: Record<string, SharedMcpEngineCell>;
}

/** 引擎元数据行（sharedmcp.EngineMeta，engines.go）：四引擎 + omp excluded */
export interface SharedMcpEngineMeta {
  id: string;
  name: string;
  /** "ok"（可分发）| "excluded"（无 MCP 配置面，仅 omp） */
  support: string;
  detail?: string;
  /** 该引擎 MCP 配置文件路径（omp 为空） */
  targetPath?: string;
  /** 当前由本应用托管（Applied）的条目数 */
  managedCount: number;
}

/** 面板首屏全量状态视图（main.SharedMcpOverview，内嵌 sharedmcp.Overview） */
export interface SharedMcpOverview {
  entries: SharedMcpEntryOverview[];
  registryExists: boolean;
  registryPath: string;
  enginesMeta: SharedMcpEngineMeta[];
}

/** 枚举登记条目并实测各引擎分发状态（含 drift/conflict/unsupported） */
export function listSharedMcp(): Promise<SharedMcpOverview> {
  return callApi('[api.sharedMcp.listSharedMcp]', () => ListSharedMcp());
}

/** 新增登记条目（同名已存在后端拒绝），返回最新 Overview */
export function createSharedMcpEntry(entry: SharedMcpEntry): Promise<SharedMcpOverview> {
  return callApi('[api.sharedMcp.createSharedMcpEntry]', () => CreateSharedMcpEntry(entry));
}

/**
 * 更新登记条目（name 是标识不可改；只改登记文件，不自动重分发——
 * 已分发引擎随内容变化如实转 drift，由用户逐引擎重新分发）
 */
export function updateSharedMcpEntry(
  name: string,
  entry: SharedMcpEntry,
): Promise<SharedMcpOverview> {
  return callApi('[api.sharedMcp.updateSharedMcpEntry]', () =>
    UpdateSharedMcpEntry(name, entry),
  );
}

/**
 * 删除登记条目（Applied 非空时先逐引擎还原，任一失败整体失败且状态如实）；
 * force 供外部手改条目的二次确认后重试
 */
export function deleteSharedMcpEntry(name: string, force: boolean): Promise<SharedMcpOverview> {
  return callApi('[api.sharedMcp.deleteSharedMcpEntry]', () =>
    DeleteSharedMcpEntry(name, force),
  );
}

/**
 * 条目 × 引擎开关：selected=true = Distribute（幂等 resync，drift 格的
 * 「重新分发」即调本方法）；false = Undistribute（移除我方托管条目）。
 */
export function setSharedMcpEngineSelection(
  name: string,
  engine: string,
  selected: boolean,
  force: boolean,
): Promise<SharedMcpOverview> {
  return callApi('[api.sharedMcp.setSharedMcpEngineSelection]', () =>
    SetSharedMcpEngineSelection(name, engine, selected, force),
  );
}

/**
 * 打开登记文件所在目录（系统文件管理器）。契约 §8 明确不为共用 MCP 新增
 * 「打开目录」门面，此处复用既有通用门面 OpenFileInEditor（目录经系统默认
 * 程序 = 文件管理器打开，内部先 os.Stat 校验存在）。
 */
export function openSharedMcpRegistryDir(registryPath: string): Promise<void> {
  // 取父目录（兼容 Windows 反斜杠与 POSIX 正斜杠；无分隔符时原样透传由后端校验）
  const dir = registryPath.replace(/[\\/][^\\/]*$/, '');
  return callApi('[api.sharedMcp.openSharedMcpRegistryDir]', () => OpenFileInEditor(dir, 0));
}

// ---- 导入（粘贴 JSON / 选择 JSON 文件）----

/**
 * 导入预览行（sharedmcp.ImportPreviewEntry）：只含名称/归一形态/状态与
 * 附注，绝不含配置值（command/url/env/headers 等一律不回传）。
 */
export interface SharedMcpImportPreviewEntry {
  /** 条目名 */
  name: string;
  /** 归一形态 "stdio"|"http"；无法归一时为空串 */
  type: string;
  /** "ok"（可导入）| "exists"（同名将跳过）| "invalid"（无法登记，见 note） */
  status: 'ok' | 'exists' | 'invalid' | string;
  /** 状态附注（同名冲突说明 / 无法登记原因 / sse 归一说明） */
  note?: string;
}

/** 粘贴文本的解析预览结果（不落盘） */
export interface SharedMcpImportPreview {
  entries: SharedMcpImportPreviewEntry[];
}

/** 文件选择预览；原始配置留在后端 */
export interface SharedMcpImportFilePreview {
  entries: SharedMcpImportPreviewEntry[];
  /** 所选文件名（UI 展示来源用） */
  fileName: string;
  ticket: string;
}

/** 批量提交时被跳过条目的逐条报告（部分冲突报告） */
export interface SharedMcpImportSkipped {
  name: string;
  reason: string;
}

/** 显式提交的结果报告 + 最新 Overview */
export interface SharedMcpImportOutcome {
  imported: string[];
  skipped: SharedMcpImportSkipped[];
  overview: SharedMcpOverview;
}

/**
 * 解析粘贴的 JSON 并返回预览（不落盘）：只含名称/类型/状态，不含任何
 * 配置值；与现行登记比对标注同名冲突（exists）。
 */
export function parseSharedMcpImportText(text: string): Promise<SharedMcpImportPreview> {
  return callApi('[api.sharedMcp.parseSharedMcpImportText]', () =>
    ParseSharedMcpImportText(text),
  );
}

/**
 * 弹出系统文件选择对话框（*.json）并解析预览：读取仅针对对话框所选路径
 * 且有大小上限；用户取消返回 null。选文件即预览，提交仍需显式点击导入。
 */
export function pickSharedMcpImportFile(): Promise<SharedMcpImportFilePreview | null> {
  return callApi('[api.sharedMcp.pickSharedMcpImportFile]', () =>
    PickSharedMcpImportFile(),
  );
}

/**
 * 显式提交导入：后端重新解析（以提交时登记现状裁定同名冲突）批量登记，
 * 默认无分发，返回导入/跳过报告 + 最新 Overview（免二次请求）。
 */
export function importSharedMcpEntries(text: string): Promise<SharedMcpImportOutcome> {
  return callApi('[api.sharedMcp.importSharedMcpEntries]', () =>
    ImportSharedMcpEntries(text),
  );
}

export function importPickedSharedMcpFile(ticket: string): Promise<SharedMcpImportOutcome> {
  return callApi('[api.sharedMcp.importPickedSharedMcpFile]', () => ImportPickedSharedMcpFile(ticket));
}
