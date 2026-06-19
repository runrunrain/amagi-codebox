<!--
  MCPListEditor - MCP Servers 配置编辑器
  支持对象形式的 MCP servers 配置编辑
-->
<template>
  <div class="mcp-list-editor">
    <div v-if="serverList.length === 0" class="mle-empty">
      <EmptyState icon="—" title="未配置 MCP Servers" description="当前无 MCP 服务器配置" />
    </div>
    <div v-else class="mle-items">
      <div
        v-for="(item, index) in serverList"
        :key="item.key"
        class="mle-item"
      >
        <div class="mle-header">
          <span class="mle-name">{{ item.key }}</span>
          <button class="mle-remove" @click="removeServer(item.key)" title="删除">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <div class="mle-fields">
          <TextInput
            :model-value="item.server.command || ''"
            placeholder="Command"
            @update:model-value="updateServer(item.key, 'command', $event)"
          />
          <TextInput
            :model-value="formatArgs(item.server.args)"
            placeholder="Args (comma separated)"
            @update:model-value="updateServer(item.key, 'args', $event)"
          />
        </div>
      </div>
      <AppButton variant="ghost" size="small" @click="addServer">
        + 添加 MCP Server
      </AppButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import TextInput from '../ui/TextInput.vue';
import AppButton from '../ui/AppButton.vue';
import EmptyState from '../ui/EmptyState.vue';

interface MCPServer {
  command?: string;
  args?: string | string[];
  [key: string]: any;
}

interface ServerItem {
  key: string;
  server: MCPServer;
}

interface Props {
  mcpServers: Record<string, MCPServer> | MCPServer[];
}

const props = defineProps<Props>();

const emit = defineEmits<{
  'update': [servers: any];
}>();

const serverList = computed<ServerItem[]>(() => {
  const data = props.mcpServers || {};
  if (Array.isArray(data)) {
    return (data as MCPServer[]).map((server, index) => ({
      key: `server_${index}`,
      server: server || {},
    }));
  }
  return Object.entries(data as Record<string, MCPServer>).map(([key, server]) => ({
    key,
    server: server || {},
  }));
});

function formatArgs(args: string | string[] | undefined): string {
  if (!args) return '';
  if (Array.isArray(args)) return args.join(', ');
  return String(args);
}

function updateServer(key: string, field: string, value: string) {
  const data = props.mcpServers || {};

  if (Array.isArray(data)) {
    const arr = [...(data as MCPServer[])];
    const index = Number(key.replace('server_', ''));
    if (!arr[index]) arr[index] = {};
    const server = { ...arr[index] };

    if (field === 'args') {
      server[field] = value ? value.split(',').map((s) => s.trim()).filter((s) => s) : [];
    } else {
      server[field] = value;
    }

    arr[index] = server;
    emit('update', arr);
    return;
  }

  const servers = { ...(data as Record<string, MCPServer>) };
  if (!servers[key]) servers[key] = {};
  const server = { ...servers[key] };

  if (field === 'args') {
    server[field] = value ? value.split(',').map((s) => s.trim()).filter((s) => s) : [];
  } else {
    server[field] = value;
  }

  servers[key] = server;
  emit('update', servers);
}

function addServer() {
  const data = props.mcpServers || {};

  if (Array.isArray(data)) {
    const arr = [...(data as MCPServer[])];
    arr.push({});
    emit('update', arr);
  } else {
    const keys = Object.keys(data as Record<string, MCPServer>);
    const newKey = `server_${keys.length + 1}`;
    const servers = { ...(data as Record<string, MCPServer>) };
    servers[newKey] = {};
    emit('update', servers);
  }
}

function removeServer(key: string) {
  const data = props.mcpServers || {};

  if (Array.isArray(data)) {
    const index = Number(key.replace('server_', ''));
    const arr = (data as MCPServer[]).filter((_, i) => i !== index);
    emit('update', arr);
  } else {
    const servers = { ...(data as Record<string, MCPServer>) };
    delete servers[key];
    emit('update', servers);
  }
}
</script>

<style scoped>
.mcp-list-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.mle-empty {
  padding: 10px 0;
}

.mle-items {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.mle-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  background: var(--control);
  border-radius: 8px;
}

.mle-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.mle-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--label);
}

.mle-remove {
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

.mle-remove:hover {
  background: rgba(255, 59, 48, 0.1);
}

.mle-remove svg {
  width: 14px;
  height: 14px;
}

.mle-fields {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
</style>
