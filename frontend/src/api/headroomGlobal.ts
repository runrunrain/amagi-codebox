/**
 * Headroom Global API (codex 全局压缩独立实例)
 * 封装独立 codex 桌面版/CLI/IDE 的 Headroom 全局压缩开关与状态查询。
 *
 * 与会话级 headroom.ts（anthropic 目标、端口 8787）完全隔离：
 * 本模块对应独立第二 headroom 实例（OpenAI 目标、端口 8788），
 * 持久化字段由 settings.Service 管理，生命周期由 main.App 编排。
 *
 * 直接包装 wailsjs/go/main/App 的 GetCodexGlobalHeadroom / SetCodexGlobalHeadroom。
 */

import {
  GetCodexGlobalHeadroom,
  SetCodexGlobalHeadroom,
} from '../../wailsjs/go/main/App';

import { main } from '../../wailsjs/go/models';

// 类型别名：暴露给上层组件使用
type CodexGlobalHeadroomStatus = main.CodexGlobalHeadroomStatus;

/**
 * 读取 codex 全局 headroom 的持久化状态 + 第二实例存活标志。
 * - enabled / target / port: 持久化开关与配置（settings.json）
 * - running: 第二 headroom 实例当前是否存活（运行时探测，非持久化）
 *
 * 后端回退策略：target 空回退 https://api.openai.com/v1，port<=0 回退 8788。
 */
export async function getCodexGlobalHeadroom(): Promise<CodexGlobalHeadroomStatus> {
  try {
    return await GetCodexGlobalHeadroom();
  } catch (error) {
    console.error('[api.headroomGlobal.getCodexGlobalHeadroom]', error);
    throw error;
  }
}

/**
 * 启用/禁用 codex 全局 headroom。
 *
 * 启用语义：
 * - 启动独立第二 headroom 实例（OpenAI 目标，端口 8788）
 * - 写入 ~/.codex/config.toml 的 openai_base_url 标记块（独立于现有 amagi-codebox-inject）
 * - 持久化 enabled/target/port 到 settings.json
 * - 对独立 codex 桌面版/CLI/IDE 同时生效
 *
 * 禁用语义：反向清理（停实例 + 清标记块 + 持久化关闭）。
 *
 * @param enabled  目标开关状态
 * @param target   OpenAI upstream base URL；空串由后端回退到 https://api.openai.com/v1
 * @param port     第二实例监听端口；<=0 由后端回退到 8788
 * @returns        最新状态（含 running 探测结果）
 */
export async function setCodexGlobalHeadroom(
  enabled: boolean,
  target: string,
  port: number,
): Promise<CodexGlobalHeadroomStatus> {
  try {
    return await SetCodexGlobalHeadroom(enabled, target, port);
  } catch (error) {
    console.error('[api.headroomGlobal.setCodexGlobalHeadroom]', error);
    throw error;
  }
}

export type { CodexGlobalHeadroomStatus };
