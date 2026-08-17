/**
 * Skins API（皮肤/壁纸，plan.md 切片 B）
 * 包装 internal/skins Wails 绑定：本地图片导入、皮肤库管理、皮肤设置读写。
 * 图片经 AssetHandler 以 /skins/<fileName> 只读提供，Skin.url 可直接作 img src / CSS url()。
 */

import {
  PickSkinImage,
  ImportSkinImage,
  ListSkins,
  RemoveSkin,
  GetSkinSettings,
  SetSkinSettings,
} from '../../wailsjs/go/skins/Service';

import { settings, skins } from '../../wailsjs/go/models';
import { callApi } from './internal/call';

export type Skin = skins.Skin;
export type SkinSettings = settings.SkinSettings;

/** 调系统文件对话框选择并导入一张图片（png/jpeg/webp，≤20MB，魔数校验）；取消时后端返回空，前端按 null 处理。 */
export async function pickSkinImage(): Promise<Skin | null> {
  const skin = await callApi('[api.skins.pickSkinImage]', () => PickSkinImage());
  return skin && skin.id ? skin : null;
}

/** 从给定路径导入图片（不经对话框）。 */
export function importSkinImage(path: string): Promise<Skin> {
  return callApi('[api.skins.importSkinImage]', () => ImportSkinImage(path));
}

/** 列出皮肤库中全部已导入皮肤。 */
export function listSkins(): Promise<Skin[]> {
  return callApi('[api.skins.listSkins]', () => ListSkins());
}

/** 删除皮肤；当前被应用（enabled）的皮肤会被后端拒绝。 */
export function removeSkin(id: string): Promise<void> {
  return callApi('[api.skins.removeSkin]', () => RemoveSkin(id));
}

/** 读取皮肤设置（enabled/imageId/dim/blur）。 */
export function getSkinSettings(): Promise<SkinSettings> {
  return callApi('[api.skins.getSkinSettings]', () => GetSkinSettings());
}

/** 保存皮肤设置（后端 clamp 区间；enabled 时校验 imageId 存在于皮肤库）。 */
export function setSkinSettings(s: SkinSettings): Promise<void> {
  return callApi('[api.skins.setSkinSettings]', () => SetSkinSettings(s));
}
