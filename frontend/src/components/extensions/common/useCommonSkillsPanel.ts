/**
 * 公共技能面板交互逻辑（CommonSkillsPanel 的可测内核）。
 *
 * 职责：聚合视图加载（loadAggregated + loadSkills 回填 manual targets）、
 * zip/文件夹导入、启停、targets 勾选、删除确认流、「查看插件」跳转。
 * toast 约定：成功/失败均反馈；变更类 action 失败由 store 保持旧值
 * （面板不乐观更新），回显即自动回滚。
 */

import { computed, ref } from 'vue';
import { useSharedExtStore } from '../../../stores/sharedExt';
import { usePluginStore } from '../../../stores/plugin';
import { useToast } from '../../../composables/useToast';
import { pickSkillDir, pickSkillZip, SHARED_EXT_ENGINES } from '../../../api/sharedExt';
import type { SharedExtEngine } from '../../../api/sharedExt';
import { nextTargets, skillRowModel } from './commonCenterModel';
import type { SkillRowModel } from './commonCenterModel';

export function useCommonSkillsPanel() {
  const store = useSharedExtStore();
  const pluginStore = usePluginStore();
  const toast = useToast();

  // 首次加载门（后续 store.loading 是跨 action 合并 busy，会随每次变更刷新闪动，
  // 不适合当整页 LoadingState 的开关；按钮禁用仍读 store.loading）
  const initialLoading = ref(true);
  const initialError = ref('');

  /** manual 行 targets 回填：registry skills 清单按 name 索引。 */
  const registryTargets = computed(() => {
    const map = new Map<string, string[]>();
    for (const s of store.skills) map.set(s.name, s.targets ?? []);
    return map;
  });

  const rows = computed<SkillRowModel[]>(() =>
    store.aggregated.map((entry) => skillRowModel(entry, registryTargets.value.get(entry.name))),
  );

  /** 导入/删除等单键 busy（按动作隔离，防重复提交）。 */
  const importingZip = ref(false);
  const importingDir = ref(false);
  const deleting = ref(false);

  async function refresh(force = false): Promise<void> {
    // store.error 是两个 load 共享的单槽且成功方会清空它——并发时失败错误
    // 可能被另一个成功 load 抹掉。顺序执行逐个捕获，保证错误确定性；
    // 聚合（主数据源）错误优先展示，registry 加载失败降级为空 targets。
    await store.loadSkills();
    const skillsError = store.error;
    await store.loadAggregated(force);
    const aggError = store.error;
    initialError.value = aggError || skillsError;
  }

  async function initialLoad(): Promise<void> {
    initialLoading.value = true;
    try {
      await refresh();
    } finally {
      initialLoading.value = false;
    }
  }

  async function handleRetry(): Promise<void> {
    await initialLoad();
  }

  async function handleImportZip(): Promise<void> {
    if (importingZip.value) return;
    const path = await pickSkillZip();
    if (!path) return; // 用户取消
    importingZip.value = true;
    try {
      const imported = await store.importSkillFromZip(path);
      toast.showSuccess(`已导入 ${imported.length} 个技能`);
      await store.loadAggregated(); // registry 写路径已内建失效服务端 30s TTL 缓存
    } catch (e) {
      console.error('[CommonSkillsPanel] import zip failed:', e);
      toast.showError(`导入失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      importingZip.value = false;
    }
  }

  async function handleImportDir(): Promise<void> {
    if (importingDir.value) return;
    const path = await pickSkillDir();
    if (!path) return; // 用户取消
    importingDir.value = true;
    try {
      const imported = await store.importSkillFromDir(path);
      toast.showSuccess(`已导入 ${imported.length} 个技能`);
      await store.loadAggregated();
    } catch (e) {
      console.error('[CommonSkillsPanel] import dir failed:', e);
      toast.showError(`导入失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      importingDir.value = false;
    }
  }

  async function handleToggleEnabled(name: string, enabled: boolean): Promise<void> {
    if (store.loading) return;
    try {
      await store.setSkillEnabled(name, enabled);
      toast.showSuccess(enabled ? `已启用「${name}」` : `已停用「${name}」`);
      await store.loadAggregated();
    } catch (e) {
      console.error('[CommonSkillsPanel] toggle enabled failed:', e);
      toast.showError(`操作失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /**
   * targets 勾选变更：即时保存（nextTargets 计算新集合 → setSkillTargets）。
   * 失败时 store.skills 未变，勾选态随 computed 回滚，仅补 toast。
   */
  async function handleToggleTarget(name: string, engine: SharedExtEngine): Promise<void> {
    const current = registryTargets.value.get(name) ?? [];
    const next = nextTargets(current, engine);
    try {
      await store.setSkillTargets(name, next);
    } catch (e) {
      console.error('[CommonSkillsPanel] set targets failed:', e);
      toast.showError(`保存分发目标失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // ----- 删除确认流 -----
  const deleteTarget = ref<SkillRowModel | null>(null);
  const showDeleteDialog = computed({
    get: () => deleteTarget.value !== null,
    set: (open: boolean) => {
      if (!open) deleteTarget.value = null;
    },
  });

  function requestDelete(row: SkillRowModel): void {
    if (!row.managed) return; // 插件条目删除统一走插件管理（P4）
    deleteTarget.value = row;
  }

  async function confirmDelete(): Promise<void> {
    const row = deleteTarget.value;
    if (!row || deleting.value) return;
    deleting.value = true;
    try {
      await store.deleteSkill(row.name);
      toast.showSuccess(`已删除「${row.name}」`);
      deleteTarget.value = null;
      await store.loadAggregated();
    } catch (e) {
      console.error('[CommonSkillsPanel] delete failed:', e);
      toast.showError(`删除失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      deleting.value = false;
    }
  }

  /**
   * 「查看插件」跳转：切到插件管理 tab + 对应引擎 + 尽力选中该插件
   * （activePluginId 仅 Claude/Codex 列表消费；先 engine 后 id——
   * setPluginEngine 会清空 activePluginId，顺序不能反）。
   */
  function viewPlugin(row: SkillRowModel): void {
    const engine = row.pluginEngine;
    const engines: readonly string[] = ['claude', 'opencode', 'codex', 'pi', 'omp'];
    if (!engine || !engines.includes(engine)) return;
    pluginStore.setExtMainTab('plugins');
    pluginStore.setPluginEngine(engine as 'claude' | 'opencode' | 'codex' | 'pi' | 'omp');
    if (row.pluginId) {
      pluginStore.setActivePlugin(row.pluginId);
    }
  }

  return {
    // 数据
    rows,
    store,
    initialLoading,
    initialError,
    engines: SHARED_EXT_ENGINES,
    // busy
    importingZip,
    importingDir,
    deleting,
    // 动作
    initialLoad,
    handleRetry,
    refresh,
    handleImportZip,
    handleImportDir,
    handleToggleEnabled,
    handleToggleTarget,
    requestDelete,
    confirmDelete,
    viewPlugin,
    // 删除确认
    deleteTarget,
    showDeleteDialog,
  };
}
