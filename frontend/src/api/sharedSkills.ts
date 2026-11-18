/**
 * Shared Skills API（共用技能，契约 §2.5/§2.6）
 * 包装再生后的 wailsjs App 门面（app_sharedskills.go 的方法），
 * 全部走 callApi 统一 "log + rethrow" 语义（api/internal/call.ts 既有范式）。
 *
 * 注意：ImportSharedSkillsZip/Dir 与 PickSharedSkillsZip/Dir 为 canonical 导入
 * 纵切新增门面，wailsjs 绑定由 Leader 统一生成（再生前同库内
 * legacySharedExtensions.ts 一样暂按命名 import 引用方案，不改生成物）。
 */

import {
  ListSharedSkills,
  SetSharedSkillEnabled,
  DeleteSharedSkill,
  OpenSharedSkillFolder,
  OpenSharedSkillsRoot,
  ImportSharedSkillsZip,
  ImportSharedSkillsDir,
  PickSharedSkillsZip,
  PickSharedSkillsDir
} from '../../wailsjs/go/main/App';

import { callApi } from './internal/call';

/**
 * 共用技能条目视图（~/.agents/skills 与 skills-disabled 两个根的目录合并视图）。
 * 字段对齐 App 门面响应 app_sharedskills.go 的 SharedSkillView
 * （sharedskills.Skill + App 层组装的 Managed/ManagedStatus）；
 * 本地声明 interface 而非 import wailsjs models，避免手写漂移（契约 §2.6）。
 */
export interface SharedSkillView {
  /** 目录名（稳定标识，basename 校验过） */
  name: string;
  /** frontmatter name，缺失回退目录名 */
  displayName: string;
  description: string;
  /** frontmatter metadata.version，缺失为空串（不伪造版本号） */
  version: string;
  author: string;
  /** 位于 skills/ → true；skills-disabled/ → false */
  enabled: boolean;
  /** 技能目录绝对路径 */
  path: string;
  /** SKILL.md 缺失时元数据为回退值并如实标记 */
  hasSkillMd: boolean;
  /** amagi-codebox 自带技能（应用托管，不可启停/删除） */
  managed: boolean;
  /** install 五态（not-installed/up-to-date/outdated/modified-by-user/invalid），仅 managed 有值 */
  managedStatus?: string;
  marketOrigin?: { marketplace: string; plugin: string };
}

/** 枚举共用技能（两根目录皆缺时返回空切片、不报错） */
export function listSharedSkills(): Promise<SharedSkillView[]> {
  return callApi('[api.sharedSkills.listSharedSkills]', () => ListSharedSkills());
}

/** 启停 = 目录在 skills/ ⇄ skills-disabled/ 之间移动，返回最新列表（前端免二次请求） */
export function setSharedSkillEnabled(name: string, enabled: boolean): Promise<SharedSkillView[]> {
  return callApi('[api.sharedSkills.setSharedSkillEnabled]', () =>
    SetSharedSkillEnabled(name, enabled),
  );
}

/** 永久删除技能目录（含禁用态目录），返回最新列表 */
export function deleteSharedSkill(name: string): Promise<SharedSkillView[]> {
  return callApi('[api.sharedSkills.deleteSharedSkill]', () => DeleteSharedSkill(name));
}

/** 打开单个技能所在目录（系统文件管理器） */
export function openSharedSkillFolder(name: string): Promise<void> {
  return callApi('[api.sharedSkills.openSharedSkillFolder]', () => OpenSharedSkillFolder(name));
}

/** 打开 ~/.agents/skills 技能根目录 */
export function openSharedSkillsRoot(): Promise<void> {
  return callApi('[api.sharedSkills.openSharedSkillsRoot]', () => OpenSharedSkillsRoot());
}

/** 导入结果信封：imported 为刚导入的技能，views 为附带来源及托管信息的完整列表。 */
export interface SharedSkillsImportResult {
  /** 本次成功导入的技能（落点 ~/.agents/skills-disabled，默认停用） */
  imported: Omit<SharedSkillView, 'managed' | 'managedStatus' | 'marketOrigin'>[];
  /** 跳过明细（同名拒绝覆盖/非法名/超限/托管名保留），部分成功时非空 */
  skipped: string[];
  /** 导入后的最新列表（前端免二次请求） */
  views: SharedSkillView[];
}

/** 从 zip 压缩包导入（部分成功不报错，明细在 result.skipped；仅整体失败才 reject） */
export function importSharedSkillsZip(path: string): Promise<SharedSkillsImportResult> {
  return callApi('[api.sharedSkills.importSharedSkillsZip]', () => ImportSharedSkillsZip(path));
}

/** 从本地文件夹导入（自身含 SKILL.md 视为单个，否则扫描子目录）；语义同 zip 导入 */
export function importSharedSkillsDir(path: string): Promise<SharedSkillsImportResult> {
  return callApi('[api.sharedSkills.importSharedSkillsDir]', () => ImportSharedSkillsDir(path));
}

/** 弹系统对话框选技能 zip；用户取消返回空串 */
export function pickSharedSkillsZip(): Promise<string> {
  return callApi('[api.sharedSkills.pickSharedSkillsZip]', () => PickSharedSkillsZip());
}

/** 弹系统对话框选技能文件夹；用户取消返回空串 */
export function pickSharedSkillsDir(): Promise<string> {
  return callApi('[api.sharedSkills.pickSharedSkillsDir]', () => PickSharedSkillsDir());
}
