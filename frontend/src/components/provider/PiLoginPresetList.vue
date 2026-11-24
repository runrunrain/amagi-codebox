<template>
  <div class="preset-panel">
    <LoadingState v-if="loading" message="加载 Pi 登录预设中..." />
    <ErrorState v-else-if="loadError" title="读取 Pi 登录预设失败" :message="loadError" :on-retry="loadPresets" />
    <template v-else>
      <div class="pc-toolbar">
        <div class="pc-filter" role="group" aria-label="按登录来源筛选">
          <Chip :active="!providerFilter" @click="providerFilter = ''">全部</Chip>
          <Chip v-for="provider in sources" :key="provider" :active="providerFilter === provider" @click="providerFilter = provider">
            {{ provider }}
          </Chip>
        </div>
        <div class="login-actions">
          <AppButton variant="ghost" size="small" :disabled="checkingLogin" @click="refreshLogin">
            {{ checkingLogin ? '检查登录状态中…' : '刷新登录状态' }}
          </AppButton>
          <AppButton variant="primary" size="small" :disabled="!canAdd || deleting" @click="openEditor(null)">+ 添加预设</AppButton>
        </div>
      </div>

      <p v-if="!checkingLogin && store.loginLoadError" role="status" class="login-note">
        读取 Pi 登录状态失败：{{ store.loginLoadError }}。仍可编辑或删除已保存的预设。
      </p>
      <p v-else-if="!checkingLogin && !store.loginProviders.length" class="login-note">
        未发现 Pi 登录。请先在 Pi 中完成 /login，再刷新状态以添加预设。
      </p>

      <ul v-if="visiblePresets.length" class="preset-group">
        <li v-for="[key, preset] in visiblePresets" :key="key" class="preset-row">
          <div class="preset-head">
            <button type="button" class="preset-open" :aria-label="`编辑预设 ${preset.name}`" :disabled="deleting" @click="openEditor(preset)">
              <span class="preset-name">{{ preset.name }}</span>
            </button>
            <span class="preset-prov">{{ preset.provider }}</span>
            <span v-if="!checkingLogin && !availableSources.has(preset.provider)" class="preset-prov">
              {{ store.loginLoadError ? '登录状态未知' : '未发现登录' }}
            </span>
            <button type="button" class="preset-delete" :aria-label="`删除预设 ${preset.name}`" :disabled="deleting" @click="requestDelete(key, preset)">删除</button>
          </div>
          <div class="preset-badges">
            <span class="param model">{{ preset.model }}</span>
            <span class="param key">思考 · {{ preset.thinking || 'Pi 默认' }}</span>
            <span v-if="preset.context_window" class="param">窗口 {{ preset.context_window.toLocaleString() }}</span>
            <span v-if="preset.max_tokens" class="param">输出 {{ preset.max_tokens.toLocaleString() }}</span>
          </div>
        </li>
      </ul>
      <EmptyState v-else icon="≡" :title="providerFilter ? `${providerFilter} 暂无登录预设` : '暂无 Pi 登录预设'">
        <template #action>
          <AppButton v-if="providerFilter" variant="ghost" size="small" class="empty-action" @click="providerFilter = ''">显示全部</AppButton>
          <AppButton v-else-if="canAdd" variant="primary" size="small" class="empty-action" @click="openEditor(null)">添加预设</AppButton>
        </template>
      </EmptyState>
    </template>
  </div>

  <PiLoginPresetDialog
    v-model:open="editorOpen"
    :preset="editingPreset"
    :preferred-provider="providerFilter"
    :providers="store.loginProviders"
    :can-add="canAdd"
    :presets="presets"
    @saved="handleSaved"
  />
  <ConfirmDialog
    v-model:open="deleteOpen"
    title="删除预设"
    :message="`确定删除「${deletingPreset?.name ?? ''}」吗？此操作不会退出 Pi 登录。`"
    danger
    confirm-text="删除"
    @confirm="confirmDelete"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { deletePiLoginPreset, getPiLoginPresets, type PiLoginPreset } from '../../api/piLogin';
import { useToast } from '../../composables/useToast';
import { useProviderStore } from '../../stores/provider';
import AppButton from '../ui/AppButton.vue';
import Chip from '../ui/Chip.vue';
import ConfirmDialog from '../ui/ConfirmDialog.vue';
import EmptyState from '../ui/EmptyState.vue';
import ErrorState from '../ui/ErrorState.vue';
import LoadingState from '../ui/LoadingState.vue';
import PiLoginPresetDialog from './PiLoginPresetDialog.vue';

const props = defineProps<{ providerFilter: string }>();
const emit = defineEmits<{ 'update:providerFilter': [provider: string] }>();
const providerFilter = computed({
  get: () => props.providerFilter,
  set: provider => emit('update:providerFilter', provider),
});
const store = useProviderStore();
const { showSuccess, showError } = useToast();
const presets = ref<Record<string, PiLoginPreset>>({});
const loading = ref(true);
const loadError = ref('');
const checkingLogin = ref(true);
const editorOpen = ref(false);
const editingPreset = ref<PiLoginPreset | null>(null);
const deleteOpen = ref(false);
const deleting = ref(false);
const deletingPreset = ref<{ key: string; name: string } | null>(null);

const availableSources = computed(() => new Set(store.loginProviders.map(p => p.provider)));
const canAdd = computed(() => !checkingLogin.value && !store.loginLoadError && availableSources.value.size > 0);
const sources = computed(() => [...new Set([
  ...availableSources.value,
  ...Object.values(presets.value).map(p => p.provider),
  ...(providerFilter.value ? [providerFilter.value] : []),
])].sort((a, b) => a.localeCompare(b)));
const visiblePresets = computed(() => Object.entries(presets.value)
  .filter(([, preset]) => !providerFilter.value || preset.provider === providerFilter.value)
  .sort(([, a], [, b]) => a.provider.localeCompare(b.provider) || a.name.localeCompare(b.name)));

async function loadPresets() {
  loading.value = true;
  loadError.value = '';
  try { presets.value = await getPiLoginPresets() || {}; }
  catch (err) { loadError.value = String(err); }
  finally { loading.value = false; }
}

async function refreshLogin() {
  checkingLogin.value = true;
  try { await store.loadLoginProviders(); }
  finally { checkingLogin.value = false; }
}

function openEditor(preset: PiLoginPreset | null) {
  editingPreset.value = preset;
  editorOpen.value = true;
}

function handleSaved(provider: string) {
  if (providerFilter.value) providerFilter.value = provider;
  void loadPresets();
}

function requestDelete(key: string, preset: PiLoginPreset) {
  deletingPreset.value = { key, name: preset.name };
  deleteOpen.value = true;
}

async function confirmDelete() {
  const target = deletingPreset.value;
  if (!target || deleting.value) return;
  deleting.value = true;
  try {
    await deletePiLoginPreset(target.key);
    showSuccess('已删除 Pi 登录预设');
    await loadPresets();
  } catch (err) {
    showError(`删除 Pi 登录预设失败：${String(err)}`);
  } finally {
    deleting.value = false;
    deletingPreset.value = null;
  }
}

onMounted(() => { void loadPresets(); void refreshLogin(); });
</script>

<style scoped src="./presetList.css"></style>
<style scoped>
.login-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.login-note { margin: 0; color: var(--secondary); font-size: 13px; }
</style>
