/**
 * Appearance Store (Pinia setup style)
 * 外观偏好状态：会话 Web 平面配色方向（webPlaneSkin：dark/light）。
 *
 * App.vue 启动时 ensureLoaded() 一次；TerminalView 消费（iframe URL 注入
 * skin=light + 宿主底色类），AppearanceSettings 编辑。读取失败保持 dark
 * 缺省（与后端 GetWebPlaneSkin 的 fail-safe 归一一致）。
 */

import { defineStore } from 'pinia';
import { ref } from 'vue';
import { getWebPlaneSkin, setWebPlaneSkin, type WebPlaneSkin } from '../api/settings';

export const useAppearanceStore = defineStore('appearance', () => {
  // === State ===
  const webPlaneSkin = ref<WebPlaneSkin>('dark');
  const loaded = ref(false);
  let loadPromise: Promise<void> | null = null;

  // === Actions ===

  /** 读取 Web 平面配色方向（并发去重：多处调用只发一次请求）。失败保持 dark 并 console.warn。 */
  function ensureLoaded(): Promise<void> {
    if (!loadPromise) {
      loadPromise = (async () => {
        try {
          webPlaneSkin.value = await getWebPlaneSkin();
        } catch (err) {
          console.warn('[stores.appearance] load webPlaneSkin failed:', err);
        } finally {
          loaded.value = true;
        }
      })();
    }
    return loadPromise;
  }

  /** 持久化并即时生效（Web 平面 iframe URL 计算属性自动跟随重载）。失败抛错给调用方提示。 */
  async function setSkin(v: WebPlaneSkin) {
    await setWebPlaneSkin(v);
    webPlaneSkin.value = v;
  }

  return {
    // State
    webPlaneSkin,
    loaded,
    // Actions
    ensureLoaded,
    setSkin,
  };
});
