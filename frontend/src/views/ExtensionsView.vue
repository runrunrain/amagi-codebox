<template>
  <section class="view-extensions">
    <PageHead title="扩展管理" description="管理 Claude、OpenCode、Codex、Pi 与 OMP 插件和环境变量"/>

    <!-- RC1-6：远程模式提示条（扩展管理为本机功能） -->
    <RemoteScopeBanner subject="扩展管理" />

    <!-- Main segmented control: Plugins | Environment | Other tools -->
    <div class="ex-main-tabs">
      <Segmented
        v-model="extMainTab"
        :options="mainTabOptions"
        variant="pill"
      />
    </div>

    <!-- Content based on main tab -->
    <div class="ex-content">
      <!-- Plugins tab -->
      <div v-if="extMainTab === 'plugins'" class="tab-pane">
        <!-- Engine segmented: ClaudeCode | OpenCode | Codex | Pi | OMP。
             引擎切换经 setPluginEngine 走 store 动作（清 activePluginId），
             并把共享 pluginView 拉回列表态（契约 §2：引擎切换重置到列表态），
             不用 v-model 直写 ref 绕过该重置。 -->
        <div class="ex-engine-tabs">
          <Segmented
            :model-value="pluginEngine"
            :options="engineOptions"
            variant="underline"
            @update:model-value="handleEngineChange"
          />
        </div>

        <!-- Engine content -->
        <div class="engine-content">
          <!-- ClaudeCode plugins -->
          <div v-if="pluginEngine === 'claude'" class="engine-pane">
            <!-- :key 区分 Claude/Codex 两分支：否则 v-if 切换时 Vue 原地 patch、
                 复用同一 PluginInstalledPanel 实例，onMounted 装载不再执行，
                 直切后目标引擎数据永不加载（走查 medium 修复） -->
            <PluginInstalledPanel
              key="claude"
              engine="claude"
              @add_market="handleAddMarket"
            />
            <div class="wsl-target-slot">
              <WSLPluginTargetPlaceholder cli="claude" />
            </div>
          </div>

          <!-- OpenCode plugins -->
          <div v-else-if="pluginEngine === 'opencode'" class="engine-pane">
            <OpenCodePluginPanel />
            <div class="wsl-target-slot">
              <WSLPluginTargetPlaceholder cli="opencode" />
            </div>
          </div>

          <!-- Pi packages -->
          <div v-else-if="pluginEngine === 'pi'" class="engine-pane">
            <PiPluginPanel />
          </div>

          <!-- OMP plugins -->
          <div v-else-if="pluginEngine === 'omp'" class="engine-pane">
            <OmpPluginPanel />
            <div class="wsl-target-slot">
              <WSLPluginTargetPlaceholder cli="omp" />
            </div>
          </div>

          <!-- Codex plugins -->
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

      <!-- Environment variables tab -->
      <div v-else-if="extMainTab === 'env'" class="tab-pane">
        <EnvVarsPanel />
      </div>

      <!-- Other tools tab -->
      <div v-else-if="extMainTab === 'tools'" class="tab-pane">
        <OtherToolsPanel />
      </div>
    </div>

    <!-- Add Marketplace Dialog -->
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
import Segmented from '../components/ui/Segmented.vue';
import PluginInstalledPanel from '../components/extensions/PluginInstalledPanel.vue';
import OpenCodePluginPanel from '../components/extensions/OpenCodePluginPanel.vue';
import PiPluginPanel from '../components/extensions/PiPluginPanel.vue';
import OmpPluginPanel from '../components/extensions/OmpPluginPanel.vue';
import WSLPluginTargetPlaceholder from '../components/extensions/WSLPluginTargetPlaceholder.vue';
import EnvVarsPanel from '../components/extensions/EnvVarsPanel.vue';
import OtherToolsPanel from '../components/extensions/OtherToolsPanel.vue';
import Dialog from '../components/ui/Dialog.vue';
import { usePluginStore } from '../stores/plugin';
import { useToast } from '../composables/useToast';

const pluginStore = usePluginStore();
const { extMainTab, pluginEngine } = storeToRefs(pluginStore);
const { showError, showSuccess } = useToast();

const {
  setPluginEngine,
  setPluginView,
  addCcMarketplace,
  addCxMarketplace,
} = pluginStore;

// Main tab options
const mainTabOptions = ref([
  { value: 'plugins', label: '插件管理' },
  { value: 'env', label: '环境变量' },
  { value: 'tools', label: '其他工具' },
]);

// Engine options
const engineOptions = ref([
  { value: 'claude', label: 'ClaudeCode' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'codex', label: 'Codex' },
  { value: 'pi', label: 'Pi' },
  { value: 'omp', label: 'OMP' },
]);

// Add marketplace dialog
const showAddMarketDialog = ref(false);
const activeEngine = ref<'claude' | 'codex'>('claude');
const marketSource = ref('');
const addMarketSubmitting = ref(false);

// 引擎切换（契约 §2）：走 setPluginEngine 动作（内部清 activePluginId），
// 并把共享 pluginView 重置回列表态，避免上一个引擎的市场/详情态串到下一个引擎
function handleEngineChange(engine: string) {
  if (engine === pluginEngine.value) return;
  setPluginEngine(engine as 'claude' | 'opencode' | 'codex' | 'pi' | 'omp');
  setPluginView('installed');
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
// marketSource ref 与 handleEngineChange 供注入输入与引擎切换接线断言
defineExpose({ handleAddMarket, submitAddMarket, marketSource, handleEngineChange });
</script>

<style scoped>
.view-extensions {
  padding: 32px 36px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.ex-main-tabs {
  margin: 14px 14px 0;
}

.ex-engine-tabs {
  display: flex;
  gap: 24px;
  background: transparent;
  border-radius: 0;
  padding: 0;
  border-bottom: 1px solid var(--separator);
  margin-bottom: 18px;
}

.ex-content {
  min-height: 400px;
}

.tab-pane {
  display: flex;
  flex-direction: column;
}

.engine-content {
  margin-top: 8px;
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
