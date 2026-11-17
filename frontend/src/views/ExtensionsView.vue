<template>
  <section class="view-extensions">
    <PageHead title="扩展管理" description="管理 Claude Code、OpenCode、Codex、Pi、OMP 的插件，以及 ~/.agents 下的共用技能、共用 MCP 与共用插件市场"/>

    <!-- RC1-6：远程模式提示条（扩展管理为本机功能） -->
    <RemoteScopeBanner subject="扩展管理" />

    <!-- 顶部页签导航（契约 §1 重构）：七子页水平页签行，选中=胶囊高亮底
         （浅色 token，Segmented pill 选中态视觉），未选中纯文字灰；无图标 -->
    <nav class="ex-tabs" aria-label="扩展子页">
      <button
        v-for="item in subPageTabs"
        :key="item.key"
        type="button"
        :class="['ex-tab', { active: extSubPage === item.key }]"
        :aria-current="extSubPage === item.key ? 'page' : undefined"
        @click="handleSubPageChange(item.key)"
      >
        {{ item.label }}
      </button>
    </nav>

    <!-- 子页内容区（全宽，列表/详情分页化由各面板自理） -->
    <div class="ex-subpage">
      <!-- 公共：共用技能（~/.agents/skills） -->
      <SharedSkillsPanel v-if="extSubPage === 'shared-skills'" />

      <!-- 公共：共用 MCP（~/.agents/.mcp.json 登记 + 四引擎分发） -->
      <SharedMcpPanel v-else-if="extSubPage === 'shared-mcp'" />

      <!-- 公共：共用插件市场（~/.agents/marketplaces 克隆管理 + 技能部署） -->
      <SharedMarketPanel v-else-if="extSubPage === 'shared-market'" />

      <!-- ClaudeCode 子页 -->
      <!-- :key 区分 Claude/Codex 两分支：否则 v-if 切换时 Vue 原地 patch、
           复用同一 PluginInstalledPanel 实例，onMounted 装载不再执行，
           直切后目标引擎数据永不加载（走查 medium 修复） -->
      <div v-else-if="extSubPage === 'claude'" class="engine-pane">
        <PluginInstalledPanel
          key="claude"
          engine="claude"
          @add_market="handleAddMarket"
        />
        <div class="wsl-target-slot">
          <WSLPluginTargetPlaceholder cli="claude" />
        </div>
      </div>

      <!-- OpenCode 子页 -->
      <div v-else-if="extSubPage === 'opencode'" class="engine-pane">
        <OpenCodePluginPanel />
        <div class="wsl-target-slot">
          <WSLPluginTargetPlaceholder cli="opencode" />
        </div>
      </div>

      <!-- Pi 子页 -->
      <div v-else-if="extSubPage === 'pi'" class="engine-pane">
        <PiPluginPanel />
      </div>

      <!-- OMP 子页 -->
      <div v-else-if="extSubPage === 'omp'" class="engine-pane">
        <OmpPluginPanel />
        <div class="wsl-target-slot">
          <WSLPluginTargetPlaceholder cli="omp" />
        </div>
      </div>

      <!-- Codex 子页 -->
      <div v-else class="engine-pane">
        <PluginInstalledPanel
          key="codex"
          engine="codex"
          @add_market="handleAddMarket"
        />
        <div class="wsl-target-slot">
          <WSLPluginTargetPlaceholder cli="codex" />
        </div>
      </div>
    </div>

    <!-- Add Marketplace Dialog（根级保留：Claude/Codex 子页面板 emit add_market 触发） -->
    <Dialog
      v-model:open="showAddMarketDialog"
      :title="`${activeEngine === 'claude' ? 'Claude' : 'Codex'} 市场`"
      :description="`输入 ${activeEngine === 'claude' ? 'Claude' : 'Codex'} marketplace 源地址`"
    >
      <div class="add-market-form">
        <div class="form-group">
          <label>市场源</label>
          <input
            v-model="marketSource"
            type="text"
            placeholder="例: owner/repo 或 https://github.com/user/marketplace.git"
            class="form-input"
            @keydown.enter="submitAddMarket"
          />
        </div>
        <div class="form-hints">
          <p class="dialog-hint">{{ activeEngine === 'claude' ? '输入 Claude marketplace 源地址，支持 GitHub 仓库、Git URL 或本地路径。' : '输入 Codex marketplace 源地址，支持 GitHub 仓库、Git URL 或本地路径。' }}</p>
          <div class="example-codes">
            <code class="example-code">https://github.com/user/marketplace.git</code>
            <code class="example-code">/Users/name/marketplace</code>
          </div>
        </div>
      </div>
      <template #footer>
        <AppButton variant="ghost" @click="showAddMarketDialog = false">取消</AppButton>
        <AppButton
          variant="primary"
          :disabled="addMarketSubmitting || !marketSource.trim()"
          @click="submitAddMarket"
        >
          添加
        </AppButton>
      </template>
    </Dialog>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { storeToRefs } from 'pinia';
import PageHead from '../components/ui/PageHead.vue';
import RemoteScopeBanner from '../components/remote/RemoteScopeBanner.vue';
import AppButton from '../components/ui/AppButton.vue';
import PluginInstalledPanel from '../components/extensions/PluginInstalledPanel.vue';
import OpenCodePluginPanel from '../components/extensions/OpenCodePluginPanel.vue';
import PiPluginPanel from '../components/extensions/PiPluginPanel.vue';
import OmpPluginPanel from '../components/extensions/OmpPluginPanel.vue';
import SharedSkillsPanel from '../components/extensions/SharedSkillsPanel.vue';
import SharedMcpPanel from '../components/extensions/SharedMcpPanel.vue';
import SharedMarketPanel from '../components/extensions/SharedMarketPanel.vue';
import WSLPluginTargetPlaceholder from '../components/extensions/WSLPluginTargetPlaceholder.vue';
import Dialog from '../components/ui/Dialog.vue';
import { usePluginStore } from '../stores/plugin';
import type { ExtSubPage } from '../stores/plugin';
import { useToast } from '../composables/useToast';

const pluginStore = usePluginStore();
const { extSubPage } = storeToRefs(pluginStore);
const { setExtSubPage, addCcMarketplace, addCxMarketplace } = pluginStore;
const { showError, showSuccess } = useToast();

// 子页页签（契约 §1.2 顶部页签化）：静态数据不进 store（选中状态/动作在
// stores/plugin.ts）；纯文字无图标，顺序=公共两页 + 五引擎
interface SubPageTab {
  key: ExtSubPage;
  label: string;
}

const subPageTabs: SubPageTab[] = [
  { key: 'shared-skills', label: '共用技能' },
  { key: 'shared-mcp', label: '共用 MCP' },
  { key: 'shared-market', label: '共用市场' },
  { key: 'claude', label: 'Claude Code' },
  { key: 'opencode', label: 'OpenCode' },
  { key: 'codex', label: 'Codex' },
  { key: 'pi', label: 'Pi' },
  { key: 'omp', label: 'OMP' },
];

// Add marketplace dialog
const showAddMarketDialog = ref(false);
const activeEngine = ref<'claude' | 'codex'>('claude');
const marketSource = ref('');
const addMarketSubmitting = ref(false);

// 子页切换（契约 §1.3/§3）：唯一写入口为 store.setExtSubPage——
// 同值守卫防重击清详情态；引擎子页联动 setPluginEngine（清 activePluginId）
// + setPluginView('installed') 重置列表态（原引擎 Segmented 契约平移）；
// shared-skills 子页不动引擎态
function handleSubPageChange(page: ExtSubPage) {
  setExtSubPage(page);
}

// Handle add market request from child component
function handleAddMarket(engine: 'claude' | 'codex') {
  activeEngine.value = engine;
  showAddMarketDialog.value = true;
  marketSource.value = '';
}

// Submit add marketplace
async function submitAddMarket() {
  const source = marketSource.value.trim();
  if (!source) return;

  addMarketSubmitting.value = true;
  try {
    if (activeEngine.value === 'claude') {
      await addCcMarketplace(source);
    } else {
      await addCxMarketplace(source);
    }
    showSuccess('市场已添加');
    // Close dialog on success
    showAddMarketDialog.value = false;
    marketSource.value = '';
  } catch (error) {
    console.error('[ExtensionsView] Add marketplace failed:', error);
    showError('添加市场失败: ' + error);
    // Keep dialog open on error so user can see what went wrong
  } finally {
    addMarketSubmitting.value = false;
  }
}

// 测试契约（契约 §8 R5b）：交互断言直调 expose 的 handler——
// handleAddMarket(engine) 打开内联 Dialog（经 ctx.teleports.body 断言），
// submitAddMarket 按引擎分派 addCcMarketplace / addCxMarketplace；
// marketSource ref 与 handleSubPageChange 供注入输入与子页切换接线断言
defineExpose({ handleAddMarket, submitAddMarket, marketSource, handleSubPageChange });
</script>

<style scoped>
.view-extensions {
  padding: 32px 36px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}

/* 顶部页签行（契约 §1 重构）：浅灰轨道 + 选中胶囊白底浮起（Segmented pill 视觉） */
.ex-tabs {
  display: flex;
  align-items: center;
  gap: 2px;
  background: var(--control);
  border-radius: 9px;
  padding: 3px;
  width: fit-content;
  max-width: 100%;
  overflow-x: auto;
}

.ex-tab {
  padding: 6px 14px;
  border: none;
  background: transparent;
  border-radius: 7px;
  cursor: pointer;
  font-size: 13px;
  color: var(--secondary);
  font-family: inherit;
  white-space: nowrap;
  transition: all 0.12s;
}

.ex-tab:hover {
  color: var(--label);
}

.ex-tab.active {
  background: var(--card);
  color: var(--label);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}

/* 子页内容区（全宽） */
.ex-subpage {
  min-width: 0;
  min-height: 400px;
}

.engine-pane {
  animation: fadeIn 0.2s ease-out;
}

.wsl-target-slot {
  margin-top: 14px;
}

@keyframes fadeIn {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* Add marketplace dialog */
.add-market-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.form-input {
  padding: 8px 12px;
  font-size: 13px;
  border: 1px solid var(--separator);
  border-radius: 8px;
  background: var(--card);
  color: var(--label);
  transition: border-color 0.15s;
}

.form-input:focus {
  outline: none;
  border-color: var(--accent);
}

.form-hints {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.dialog-hint {
  font-size: 12px;
  color: var(--secondary);
  margin: 0;
}

.example-codes {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.example-code {
  font-size: 11px;
  padding: 4px 8px;
  background: var(--control);
  border-radius: 4px;
  font-family: var(--mono);
  color: var(--secondary);
}
</style>
