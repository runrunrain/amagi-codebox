<template>
  <section class="view-extensions">
    <PageHead title="扩展管理" description="管理 Claude Code、OpenCode、Codex、Pi、OMP 的插件，以及 ~/.agents 下的共用技能与共用 MCP"/>

    <!-- RC1-6：远程模式提示条（扩展管理为本机功能） -->
    <RemoteScopeBanner subject="扩展管理" />

    <!-- ZCode 式两栏（契约 §1）：左=子页导航（公共/Agent 分组），右=子页内容区 -->
    <div class="ex-layout">
      <nav class="ex-subnav">
        <div v-for="group in navGroups" :key="group.label" class="ex-subnav-group-wrap">
          <div class="ex-subnav-group">{{ group.label }}</div>
          <div
            v-for="item in group.items"
            :key="item.key"
            :class="['ex-subnav-item', { active: extSubPage === item.key }]"
            @click="handleSubPageChange(item.key)"
          >
            <!-- 图标复用 SidebarSettings 内联 SVG path 库风格（v-html 注入，无图片/图标库） -->
            <svg
              class="ic"
              viewBox="0 0 24 24"
              fill="none"
              stroke-width="1.7"
              stroke-linecap="round"
              stroke-linejoin="round"
              v-html="item.icon"
            />
            <span class="ex-subnav-label">{{ item.label }}</span>
          </div>
        </div>
      </nav>

      <div class="ex-subpage">
        <!-- 公共：共用技能（~/.agents/skills） -->
        <SharedSkillsPanel v-if="extSubPage === 'shared-skills'" />

        <!-- 公共：共用 MCP（~/.agents/.mcp.json 登记 + 四引擎分发） -->
        <SharedMcpPanel v-else-if="extSubPage === 'shared-mcp'" />

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
import WSLPluginTargetPlaceholder from '../components/extensions/WSLPluginTargetPlaceholder.vue';
import Dialog from '../components/ui/Dialog.vue';
import { usePluginStore } from '../stores/plugin';
import type { ExtSubPage } from '../stores/plugin';
import { useToast } from '../composables/useToast';

const pluginStore = usePluginStore();
const { extSubPage } = storeToRefs(pluginStore);
const { setExtSubPage, addCcMarketplace, addCxMarketplace } = pluginStore;
const { showError, showSuccess } = useToast();

// 子页导航（契约 §1.2）：静态数据不进 store（选中状态/动作在 stores/plugin.ts）
interface SubNavItem {
  key: ExtSubPage;
  label: string;
  icon: string;
}

// 图标：层叠方块=共用技能（同 SidebarSettings「Agent 配置档」）；插头+节点=共用 MCP
// （服务器分发语义）；五 Agent 取终端/方框系几何 path 的近似语义者（opencode=终端、
// codex=方框+光标、pi=圆点组合、omp=三栏；claude 星形 sparkle 为等价替换），不引图片/图标库
const ICON_SHARED =
  '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>';
const ICON_MCP =
  '<path d="M9 7V4"/><path d="M15 7V4"/><path d="M7 7h10v4a5 5 0 0 1-10 0z"/><path d="M12 16v4"/>';
const ICON_CLAUDE =
  '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z"/>';
const ICON_OPENCODE =
  '<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>';
const ICON_CODEX =
  '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m7 9 3 3-3 3"/><line x1="13" y1="15" x2="17" y2="15"/>';
const ICON_PI =
  '<circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/>';
const ICON_OMP =
  '<rect x="3" y="3" width="18" height="18" rx="2"/><line x1="9" y1="3" x2="9" y2="21"/><line x1="15" y1="3" x2="15" y2="21"/>';

const navGroups: { label: string; items: SubNavItem[] }[] = [
  {
    label: '公共',
    items: [
      { key: 'shared-skills', label: '共用技能', icon: ICON_SHARED },
      { key: 'shared-mcp', label: '共用 MCP', icon: ICON_MCP },
    ],
  },
  {
    label: 'Agent',
    items: [
      { key: 'claude', label: 'Claude Code', icon: ICON_CLAUDE },
      { key: 'opencode', label: 'OpenCode', icon: ICON_OPENCODE },
      { key: 'codex', label: 'Codex', icon: ICON_CODEX },
      { key: 'pi', label: 'Pi', icon: ICON_PI },
      { key: 'omp', label: 'OMP', icon: ICON_OMP },
    ],
  },
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

/* ZCode 式两栏（契约 §1.4） */
.ex-layout {
  display: flex;
  gap: 22px;
  align-items: flex-start;
}

.ex-subnav {
  width: 200px;
  /* 容器 <720px 时左栏允许被压缩到 160px（label ellipsis），本期不做图标折叠 */
  min-width: 160px;
  flex-shrink: 1;
  position: sticky;
  top: 12px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ex-subnav-group {
  font-size: 11px;
  font-weight: 600;
  color: var(--tertiary);
  padding: 14px 9px 6px;
  letter-spacing: 0.4px;
}

/* 首个分组去掉顶部 padding */
.ex-subnav-group-wrap:first-child .ex-subnav-group {
  padding-top: 0;
}

/* 复刻 SidebarSettings .sb-set-item 规格（契约 §1.4） */
.ex-subnav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 9px;
  border-radius: 8px;
  cursor: pointer;
  color: var(--secondary);
  font-size: 14px;
  transition: background 0.12s;
}

.ex-subnav-item:hover {
  background: var(--control);
}

.ex-subnav-item.active {
  background: var(--control);
  color: var(--label);
}

.ex-subnav-item .ic {
  width: 17px;
  height: 17px;
  stroke: var(--tertiary);
  flex-shrink: 0;
}

.ex-subnav-item.active .ic {
  stroke: var(--accent);
}

.ex-subnav-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ex-subpage {
  flex: 1;
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
