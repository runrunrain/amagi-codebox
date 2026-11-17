/**
 * MCP 导入对话框交互逻辑（MCPImportDialog 的可测内核）。
 *
 * 两步交互：粘贴/选文件填 text → 「解析预览」previewMCPJSON（纯解析不落库，
 * 非法 JSON 就地展示错误，不污染全局 error）→ 「导入」importMCPServers
 * （skipped 非空 = 部分成功不抛错，toast 提示已跳过同名）。
 */

import { computed, ref, watch } from 'vue';
import { useSharedExtStore } from '../../../stores/sharedExt';
import { useToast } from '../../../composables/useToast';
import { pickMCPJsonFile, readTextFile } from '../../../api/sharedExt';
import type { SharedMCPServer } from '../../../api/sharedExt';
import { mcpConfigSummary } from './commonCenterModel';

/** 预览行：server 名 + command/url 摘要行。 */
export interface McpPreviewRow {
  name: string;
  summary: string;
}

export function useMCPImportDialog() {
  const store = useSharedExtStore();
  const toast = useToast();

  const open = ref(false);
  const text = ref('');
  /** 完整解析结果（导入载荷；预览行由它派生）。 */
  const parsed = ref<SharedMCPServer[]>([]);
  /** 就地预览错误（解析失败）：与全局 store.error 分离。 */
  const previewError = ref('');
  const parsing = ref(false);
  const importing = ref(false);
  const pickingFile = ref(false);

  const preview = computed<McpPreviewRow[]>(() =>
    parsed.value.map((s) => ({ name: s.name, summary: mcpConfigSummary(s.config) })),
  );

  const canParse = computed(() => text.value.trim().length > 0 && !parsing.value && !importing.value);
  const canImport = computed(
    () => parsed.value.length > 0 && !previewError.value && !importing.value && !parsing.value,
  );

  // text 变化后旧预览即时失效（防止改文本后导入旧解析结果；同步触发，
  // 保证修改后立即 canImport=false，无需等调度器）
  watch(text, () => {
    parsed.value = [];
    previewError.value = '';
  }, { flush: 'sync' });

  /** 打开/重置。 */
  function openDialog(): void {
    text.value = '';
    parsed.value = [];
    previewError.value = '';
    open.value = true;
  }

  function closeDialog(): void {
    open.value = false;
  }

  /** 「从文件导入」：选 .json → 读文本回填 textarea（预览态一并作废）。 */
  async function handlePickFile(): Promise<void> {
    if (pickingFile.value) return;
    const path = await pickMCPJsonFile();
    if (!path) return; // 用户取消
    pickingFile.value = true;
    try {
      text.value = await readTextFile(path);
      toast.showInfo('已读取文件，请解析预览');
    } catch (e) {
      console.error('[MCPImportDialog] read file failed:', e);
      toast.showError(`读取文件失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      pickingFile.value = false;
    }
  }

  /** 「解析预览」：纯解析（不落库），server 名 + 摘要列表。 */
  async function handleParse(): Promise<void> {
    if (!canParse.value) return;
    parsing.value = true;
    previewError.value = '';
    try {
      parsed.value = await store.previewMCPJSON(text.value);
      if (parsed.value.length === 0) {
        previewError.value = '未解析到任何 MCP server（应为 { "server名": {...} } 映射格式）';
      }
    } catch (e) {
      // 后端中文报错（JSON 非法/条目缺字段等）就地展示，不写全局 error
      parsed.value = [];
      previewError.value = e instanceof Error ? e.message : String(e);
    } finally {
      parsing.value = false;
    }
  }

  /**
   * 「导入」：importMCPServers 部分成功不抛错（skipped 非空），
   * 成功 toast + skipped 单独提示「已跳过同名: xxx」；imported>0 时
   * store 内部已刷新清单；仅 registry 写入失败才走 catch（对话框保留）。
   */
  async function handleImport(): Promise<void> {
    if (!canImport.value) return;
    importing.value = true;
    try {
      const result = await store.importMCPServers(parsed.value);
      toast.showSuccess(`已导入 ${result.imported.length} 个 MCP server`);
      if (result.skipped.length > 0) {
        toast.showWarn(`已跳过同名: ${result.skipped.join('、')}`);
      }
      open.value = false;
      text.value = '';
      parsed.value = [];
    } catch (e) {
      console.error('[MCPImportDialog] import failed:', e);
      toast.showError(`导入失败: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      importing.value = false;
    }
  }

  return {
    open,
    text,
    preview,
    previewError,
    parsing,
    importing,
    pickingFile,
    canParse,
    canImport,
    openDialog,
    closeDialog,
    handlePickFile,
    handleParse,
    handleImport,
  };
}
