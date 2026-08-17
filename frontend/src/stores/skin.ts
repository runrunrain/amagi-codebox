/**
 * Skin Store (Pinia setup style)
 * 皮肤/壁纸状态：settings（enabled/imageId/dim/blur/opacity）+ 皮肤库列表 + 当前皮肤。
 *
 * App.vue 启动时 load() 一次，并 watch settings/currentSkin 同步
 * --skin-image / --skin-blur / --skin-dim / --skin-panel-alpha CSS 变量与 html[data-skin]；
 * 设置页保存后即时生效，无需刷新。
 */

import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as skinsApi from '../api/skins';
import type { Skin, SkinSettings } from '../api/skins';

const DEFAULT_SETTINGS: SkinSettings = {
  enabled: false,
  imageId: '',
  dim: 35,
  blur: 0,
  opacity: 70,
} as SkinSettings;

export const useSkinStore = defineStore('skin', () => {
  // === State ===
  const settings = ref<SkinSettings>({ ...DEFAULT_SETTINGS });
  const skins = ref<Skin[]>([]);
  const loaded = ref(false);
  const saving = ref(false);

  // === Computed ===
  const currentSkin = computed<Skin | null>(() => {
    if (!settings.value.enabled || !settings.value.imageId) return null;
    return skins.value.find(s => s.id === settings.value.imageId) ?? null;
  });
  const active = computed(() => currentSkin.value !== null);

  // === Actions ===

  /** 读取皮肤设置 + 皮肤库列表，匹配出当前皮肤。失败保留现状并 console.warn。 */
  async function load() {
    try {
      const [s, list] = await Promise.all([
        skinsApi.getSkinSettings(),
        skinsApi.listSkins(),
      ]);
      settings.value = { ...DEFAULT_SETTINGS, ...s };
      skins.value = list ?? [];
      loaded.value = true;
    } catch (err) {
      console.warn('[stores.skin] load failed:', err);
    }
  }

  /** 合并补丁并持久化；成功后以服务端 clamp 结果重载。失败重载恢复原值并抛错。 */
  async function apply(patch: Partial<SkinSettings>) {
    saving.value = true;
    try {
      await skinsApi.setSkinSettings({ ...settings.value, ...patch });
      await load();
    } catch (err) {
      await load();
      throw err;
    } finally {
      saving.value = false;
    }
  }

  /** 仅本地预览（滑块拖动中），不写后端；apply 后由 load 对齐服务端值。 */
  function preview(patch: Partial<SkinSettings>) {
    settings.value = { ...settings.value, ...patch };
  }

  /** 恢复默认：关闭皮肤。 */
  async function clear() {
    await apply({
      enabled: false,
      imageId: '',
      dim: DEFAULT_SETTINGS.dim,
      blur: DEFAULT_SETTINGS.blur,
      opacity: DEFAULT_SETTINGS.opacity,
    });
  }

  /** 调系统对话框导入图片；用户取消返回 null。导入后刷新皮肤库。 */
  async function importImage(): Promise<Skin | null> {
    const skin = await skinsApi.pickSkinImage();
    if (skin) {
      skins.value = [...skins.value.filter(s => s.id !== skin.id), skin];
    }
    return skin;
  }

  /** 删除皮肤；被应用中的皮肤后端拒绝（错误信息原样上抛给视图提示）。 */
  async function remove(id: string) {
    await skinsApi.removeSkin(id);
    skins.value = skins.value.filter(s => s.id !== id);
  }

  return {
    // State
    settings,
    skins,
    loaded,
    saving,
    // Computed
    currentSkin,
    active,
    // Actions
    load,
    apply,
    preview,
    clear,
    importImage,
    remove,
  };
});
