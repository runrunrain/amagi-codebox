<!--
  PermissionListEditor - Permission 配置列表编辑器
  支持数组或对象形式的 permission 配置编辑
-->
<template>
  <div class="permission-list-editor">
    <div v-if="isEmpty" class="ple-empty">
      <EmptyState icon="—" title="未配置 Permission" description="当前无 permission 配置项" />
    </div>
    <div v-else class="ple-items">
      <template v-if="isObject">
        <div
          v-for="(perm, key) in permissions"
          :key="String(key)"
          class="ple-item"
        >
          <div class="ple-header">
            <span class="ple-key">{{ String(key) }}</span>
            <button class="ple-remove" @click="removePermission(String(key))" title="删除">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
          <div class="ple-fields">
            <TextInput
              :model-value="String(perm)"
              placeholder="Permission 值"
              @update:model-value="updatePermission(String(key), $event)"
            />
          </div>
        </div>
      </template>
      <template v-else>
        <div
          v-for="(perm, index) in permissions"
          :key="index"
          class="ple-item"
        >
          <div class="ple-header">
            <span class="ple-index">#{{ Number(index) + 1 }}</span>
            <button class="ple-remove" @click="removePermission(index)" title="删除">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
          <div class="ple-fields">
            <TextInput
              :model-value="String(perm)"
              placeholder="Permission 值"
              @update:model-value="updatePermission(index, $event)"
            />
          </div>
        </div>
      </template>
      <AppButton variant="ghost" size="small" @click="addPermission">
        + 添加 Permission
      </AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';

interface Props {
  permissions: Record<string, any> | any[];
}

const props = defineProps<Props>();

const emit = defineEmits<{
  update: [permissions: Record<string, any> | any[]];
}>();

const isObject = computed(() => !Array.isArray(props.permissions));

const isEmpty = computed(() => {
  if (isObject.value) {
    return Object.keys(props.permissions as Record<string, any>).length === 0;
  }
  return (props.permissions as any[]).length === 0;
});

function updatePermission(key: string | number, value: string) {
  if (isObject.value) {
    const updated = { ...(props.permissions as Record<string, any>) };
    updated[String(key)] = value;
    emit('update', updated);
  } else {
    const updated = [...(props.permissions as any[])];
    updated[Number(key)] = value;
    emit('update', updated);
  }
}

function addPermission() {
  if (isObject.value) {
    const keys = Object.keys(props.permissions as Record<string, any>);
    const newKey = `perm_${keys.length + 1}`;
    const updated = { ...(props.permissions as Record<string, any>) };
    updated[newKey] = '';
    emit('update', updated);
  } else {
    const updated = [...(props.permissions as any[])];
    updated.push('');
    emit('update', updated);
  }
}

function removePermission(key: string | number) {
  if (isObject.value) {
    const updated = { ...(props.permissions as Record<string, any>) };
    delete updated[String(key)];
    emit('update', updated);
  } else {
    const updated = (props.permissions as any[]).filter((_, i) => i !== Number(key));
    emit('update', updated);
  }
}
</script>

<style scoped>
.permission-list-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ple-empty {
  padding: 10px 0;
}

.ple-items {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ple-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  background: var(--control);
  border-radius: 8px;
}

.ple-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.ple-key, .ple-index {
  font-size: 12px;
  font-weight: 500;
  color: var(--tertiary);
  font-family: var(--mono);
}

.ple-remove {
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  color: var(--danger);
  cursor: pointer;
  border-radius: 4px;
  transition: background 0.15s;
}

.ple-remove:hover {
  background: rgba(255, 59, 48, 0.1);
}

.ple-remove svg {
  width: 14px;
  height: 14px;
}

.ple-fields {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
</style>
