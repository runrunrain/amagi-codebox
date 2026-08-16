/**
 * WebUI API（蓝图 T-1.6）
 * 包装 internal/webui Wails 绑定：pi 会话 Web 平面的状态探测与 URL 解析。
 * 契约 v1.0.2（amagi-pi docs/webui-protocol.md）为接口权威。
 */

import {
  GetWebUIStatus,
  ProbeWebUI,
  OpenWebPlane,
} from '../../wailsjs/go/webui/Service';

import { webui } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type WebUIStatus = webui.Status;
export type WebUIState = WebUIStatus['state'];

/** 读取缓存的会话 webui 状态（非阻塞，不发起探测）。 */
export function getWebUIStatus(sessionId: string): Promise<WebUIStatus> {
  return callApi('[api.webui.getWebUIStatus]', () => GetWebUIStatus(sessionId));
}

/** 执行一轮 /api/info 探测并返回最新状态（前端按 0.5–1s 轮询，契约 §4.1）。 */
export function probeWebUI(sessionId: string): Promise<WebUIStatus> {
  return callApi('[api.webui.probeWebUI]', () => ProbeWebUI(sessionId));
}

/** 解析可加载的 Web 平面 URL（契约 v1.0.2 §6.5：${httpBase}/#/t=<token>，fragment 承载 capability token）；不可用时 reject。 */
export function openWebPlane(sessionId: string): Promise<string> {
  return callApi('[api.webui.openWebPlane]', () => OpenWebPlane(sessionId));
}
