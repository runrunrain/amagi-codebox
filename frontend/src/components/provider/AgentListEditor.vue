<!--
  AgentListEditor - Agent 配置列表编辑器
  支持数组形式的 agent 配置编辑
-->
<template>
  <div class="agent-list-editor">
    <div v-if="agents.length === 0" class="ale-empty">
      <EmptyState icon="—" title="未配置 Agent" description="当前无 agent 配置项" />
    </div>
    <div v-else class="ale-items">
      <div
        v-for="(agent, index) in agents"
        :key="index"
        class="ale-item"
      >
        <div class="ale-header">
          <span class="ale-name">{{ agent.name || agent.key || `Agent ${index + 1}` }}</span>
          <button class="ale-remove" @click="removeAgent(index)" title="删除">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <div class="ale-fields">
          <TextInput
            :model-value="agent.name || ''"
            placeholder="Agent 名称"
            @update:model-value="updateAgent(index, 'name', $event)"
          />
          <TextInput
            :model-value="agent.command || ''"
            placeholder="Command"
            @update:model-value="updateAgent(index, 'command', $event)"
          />
        </div>
      </div>
      <AppButton variant="ghost" size="small" @click="addAgent">
        + 添加 Agent
      </AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';

interface Agent {
  name?: string;
  key?: string;
  command?: string;
}

interface Props {
  agents: Agent[];
}

const props = defineProps<Props>();

const emit = defineEmits<{
  update: [agents: Agent[]];
}>();

function updateAgent(index: number, key: string, value: string) {
  const updated = [...props.agents];
  if (!updated[index]) updated[index] = {};
  updated[index] = { ...updated[index], [key]: value };
  emit('update', updated);
}

function addAgent() {
  emit('update', [...props.agents, {}]);
}

function removeAgent(index: number) {
  const updated = props.agents.filter((_, i) => i !== index);
  emit('update', updated);
}
</script>

<style scoped>
.agent-list-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ale-empty {
  padding: 10px 0;
}

.ale-items {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ale-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  background: var(--control);
  border-radius: 8px;
}

.ale-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.ale-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.ale-remove {
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

.ale-remove:hover {
  background: rgba(255, 59, 48, 0.1);
}

.ale-remove svg {
  width: 14px;
  height: 14px;
}

.ale-fields {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
</style>
