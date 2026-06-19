<template>
  <section class="view-extensions">
    <PageHead title="扩展管理" description="管理 Claude 与 Codex 插件、工作区与环境变量"/>

    <!-- Main segmented control: Plugins | Workspaces | Environment -->
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
        <!-- Engine segmented: ClaudeCode | Codex -->
        <div class="ex-engine-tabs">
          <Segmented
            v-model="pluginEngine"
            :options="engineOptions"
            variant="underline"
          />
        </div>

        <!-- Engine content -->
        <div class="engine-content">
          <!-- ClaudeCode plugins -->
          <div v-if="pluginEngine === 'claude'" class="engine-pane">
            <PluginInstalledPanel
              engine="claude"
              @add_market="handleAddMarket"
            />
          </div>

          <!-- Codex plugins -->
          <div v-else class="engine-pane">
            <PluginInstalledPanel
              engine="codex"
              @add_market="handleAddMarket"
            />
          </div>
        </div>
      </div>

      <!-- Workspaces tab (placeholder for P5) -->
      <div v-else-if="extMainTab === 'workspaces'" class="tab-pane">
        <EmptyState icon="◫" title="工作区管理" description="工作区管理将在 P5 阶段实现"/>
      </div>

      <!-- Environment variables tab (placeholder for P5) -->
      <div v-else-if="extMainTab === 'env'" class="tab-pane">
        <EmptyState icon="⌘" title="环境变量" description="环境变量管理将在 P5 阶段实现"/>
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
            :placeholder="activeEngine === 'claude' ? '例: owner/repo 或 https://github.com/user/marketplace.git' : '例: owner/repo 或 https://github.com/user/marketplace.git'"
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
        <button class="btn secondary" @click="showAddMarketDialog = false">取消</button>
        <button
          class="btn primary"
          :disabled="addMarketSubmitting || !marketSource.trim()"
          @click="submitAddMarket"
        >
          添加
        </button>
      </template>
    </Dialog>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { storeToRefs } from 'pinia';
import PageHead from '../components/ui/PageHead.vue';
import Segmented from '../components/ui/Segmented.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import PluginInstalledPanel from '../components/extensions/PluginInstalledPanel.vue';
import Dialog from '../components/ui/Dialog.vue';
import { usePluginStore } from '../stores/plugin';

const pluginStore = usePluginStore();
const { extMainTab, pluginEngine } = storeToRefs(pluginStore);

const {
  addCcMarketplace,
  addCxMarketplace,
  loadCcMarkets,
} = pluginStore;

// Main tab options
const mainTabOptions = ref([
  { value: 'plugins', label: '插件管理' },
  { value: 'workspaces', label: '工作区管理' },
  { value: 'env', label: '环境变量' },
]);

// Engine options
const engineOptions = ref([
  { value: 'claude', label: 'ClaudeCode' },
  { value: 'codex', label: 'Codex' },
]);

// Add marketplace dialog
const showAddMarketDialog = ref(false);
const activeEngine = ref<'claude' | 'codex'>('claude');
const marketSource = ref('');
const addMarketSubmitting = ref(false);

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
    // Close dialog on success
    showAddMarketDialog.value = false;
    marketSource.value = '';
  } catch (error) {
    console.error('[ExtensionsView] Add marketplace failed:', error);
    // Keep dialog open on error so user can see what went wrong
  } finally {
    addMarketSubmitting.value = false;
  }
}
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

.btn {
  font-size: 13px;
  padding: 8px 16px;
  border-radius: 8px;
  border: none;
  cursor: pointer;
  transition: all 0.15s;
}

.btn.primary {
  background: var(--accent);
  color: #fff;
}

.btn.primary:hover:not(:disabled) {
  background: #0066cc;
}

.btn.primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn.secondary {
  background: var(--control);
  color: var(--label);
}

.btn.secondary:hover {
  background: var(--controlHover);
}
</style>
