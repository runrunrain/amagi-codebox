<template>
  <span :class="classes">{{ text }}</span>
</template>

<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  type?: 'type' | 'ver' | 'tag' | 'scope' | 'source' | 'dup' | 'warning' | 'pid';
  text: string;
  color?: string; // For type badges: integration, hybrid, skill, hook, command, agent, mcp, capability, warning, plugin
  variant?: 'muted' | 'mono';
}

const props = withDefaults(defineProps<Props>(), {
  type: 'tag',
  color: '',
});

const classes = computed(() => {
  let base = 'tag';
  if (props.type === 'ver') base = 'ver-badge';
  else if (props.type === 'scope') base = 'scope-badge';
  else if (props.type === 'source') base = 'source-badge';
  else if (props.type === 'dup' || props.type === 'warning') base = 'warning-badge';
  else if (props.type === 'pid') base = 'pid-badge';
  else if (props.type === 'type') base = 'type-badge';

  const color = props.type === 'type' && props.color ? `type-badge-${props.color}` : '';
  const variant = props.variant ? `badge-${props.variant}` : '';
  return [base, color, variant].filter(Boolean);
});
</script>

<style scoped>
.tag {
  font-size: 10px;
  font-weight: 600;
  color: #fff;
  padding: 1px 5px;
  border-radius: 4px;
  line-height: 1.4;
}

.ver-badge {
  font-family: var(--mono);
  font-size: 10px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 4px;
  padding: 1px 6px;
}

.scope-chip {
  font-size: 10px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 4px;
  padding: 1px 6px;
}

.source-badge {
  font-size: 10px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 4px;
  padding: 1px 6px;
  font-family: var(--mono);
}

.dup-badge {
  font-size: 10px;
  font-weight: 600;
  color: var(--warning);
  background: rgba(255, 149, 0, 0.14);
  border-radius: 4px;
  padding: 1px 6px;
}

/* Type badge colors */
.type-badge {
  font-size: 10px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 4px;
  line-height: 1.5;
}

.type-badge.integration {
  color: #fff;
  background: var(--accent);
}

.type-badge.hybrid {
  color: #fff;
  background: var(--purple);
}

.type-badge.skill {
  color: var(--accent);
  background: rgba(0, 122, 255, 0.1);
}

.type-badge.hook {
  color: var(--warning);
  background: rgba(255, 149, 0, 0.14);
}

.type-badge.command {
  color: var(--success);
  background: rgba(52, 199, 89, 0.14);
}

.type-badge.agent {
  color: #fff;
  background: #FF2D55;
}

.type-badge.mcp {
  color: #0a3d62;
  background: #5AC8FA;
}

.type-badge.unknown {
  color: var(--secondary);
  background: var(--control);
}

.type-badge.capability {
  color: var(--secondary);
  background: var(--control);
}

.type-badge.warning {
  color: var(--warning);
  background: rgba(255, 149, 0, 0.14);
}

.type-badge.plugin {
  color: var(--secondary);
  background: var(--control);
}

.scope-badge {
  font-size: 10px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 4px;
  padding: 1px 6px;
}

.warning-badge {
  font-size: 10px;
  font-weight: 600;
  color: var(--warning);
  background: rgba(255, 149, 0, 0.14);
  border-radius: 4px;
  padding: 1px 6px;
}

.pid-badge {
  font-size: 10px;
  color: var(--secondary);
  background: var(--control);
  border-radius: 4px;
  padding: 1px 6px;
  font-family: var(--mono);
}

.badge-muted {
  opacity: 0.8;
}

.badge-mono {
  font-family: var(--mono);
}
</style>
