/**
 * DrvFS advisory API — App.GetDrvFSAdvisories 的只读查询封装。
 *
 * 后端记录面（internal/platform/drvfs_advisory.go）由三个入口写入：
 * pty 启动路径、gitassist 形态拦截、paths 形态拦截。前端在 GitPanel /
 * PathPicker 失败处按需拉取，展示「该目录为 DrvFS 挂载」关联提示；
 * 无轮询、无后台任务。
 */

import { GetDrvFSAdvisories } from '../../wailsjs/go/main/App';
import { callApi } from './internal/call';

/** 一条 DrvFS 感知记录（与后端 platform.DrvFSAdvisory 的 JSON 形态一致）。 */
export interface DrvFSAdvisoryEntry {
  /** 写入方标识："pty" | "gitassist" | "paths"。 */
  source: string
  /** 触发感知的工作目录（保持原始形态：盘符或 Linux 形态）。 */
  workDir: string
  /** 对应的 WSL 挂载点（/mnt/<drive>/...）；非 DrvFS 形态为空串。 */
  mountPath: string
  /** 面向用户的一句话说明。 */
  detail: string
  /** 写入时刻（Unix 秒）。 */
  at: number
}

/** 拉取 advisory 快照（旧在前）；后端返回 JSON 字符串，此处解析。 */
export function getDrvFSAdvisories(): Promise<DrvFSAdvisoryEntry[]> {
  return callApi('[api.drvfsAdvisory.list]', async () => {
    const raw = await GetDrvFSAdvisories();
    return JSON.parse(raw) as DrvFSAdvisoryEntry[];
  });
}
