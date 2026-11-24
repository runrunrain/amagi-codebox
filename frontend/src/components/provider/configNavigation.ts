// 纯展示元数据：不加载组件、不读取配置文件。
export type ConfigPageId = 'pi:agents' | 'pi:registry' | 'pi:auth' | 'omp:agents' | 'omp:registry' | 'opencode:presets' | 'opencode:global';
export interface ConfigSectionItem {
  id: string; label: string; meta?: string; metaTone?: 'neutral' | 'warning'; invalid?: boolean;
}
export interface EditorNavigationSnapshot {
  owner: string; phase: 'loading' | 'ready' | 'error'; mode: 'visual' | 'source'; activeId: string; items: ConfigSectionItem[];
}
export interface EditorNavigationProps {
  sectionId?: string; navigation?: 'external' | 'local'; navOwner?: string;
}
export interface SectionNavItem extends ConfigSectionItem {
  ariaLabel?: string; disabled?: boolean; children?: SectionNavItem[]; scrollChildren?: boolean;
}
export interface SectionNavGroup { id: string; label?: string; items: SectionNavItem[] }
export const PRESET_ITEMS = [
  { value: 'anthropic', label: 'Anthropic 格式', consumers: '用于 Claude Code' },
  { value: 'openai', label: 'OpenAI 格式', consumers: '用于 Codex · Pi · OMP' },
  { value: 'pi-login', label: 'Pi 登录认证', consumers: '仅用于 Pi · 使用原生登录' },
] as const;
export type PresetPage = (typeof PRESET_ITEMS)[number]['value'];
export const CONFIG_PAGES: { id: ConfigPageId; cli: string; label: string; defaultId: string; dynamic?: boolean }[] = [
  { id: 'pi:agents', cli: 'Pi', label: 'Agent 配置', defaultId: 'agents' },
  { id: 'pi:registry', cli: 'Pi', label: '模型提供商', defaultId: '', dynamic: true },
  { id: 'pi:auth', cli: 'Pi', label: '认证登录', defaultId: '', dynamic: true },
  { id: 'omp:agents', cli: 'OMP', label: 'Agent 配置', defaultId: 'modelRoles' },
  { id: 'omp:registry', cli: 'OMP', label: '模型提供商', defaultId: '', dynamic: true },
  { id: 'opencode:presets', cli: 'OpenCode', label: '预设管理', defaultId: '' },
  { id: 'opencode:global', cli: 'OpenCode', label: '全局配置', defaultId: 'provider' },
];
export const PI_SECTIONS: ConfigSectionItem[] = [
  { id: 'agents', label: '角色模型' }, { id: 'advisor', label: '执行中咨询' }, { id: 'codemode', label: 'CodeMode' }, { id: 'compaction', label: '上下文压缩', meta: '下次压缩生效' },
  { id: 'mcp', label: 'MCP 路由' }, { id: 'concurrency', label: '并发限制' },
];
export const OMP_SECTIONS: ConfigSectionItem[] = [
  { id: 'modelRoles', label: '角色模型' }, { id: 'overrides', label: '子代理覆盖' },
];
export const OPENCODE_SECTIONS: ConfigSectionItem[] = [
  { id: 'model', label: '顶层模型' }, { id: 'provider', label: '提供商' }, { id: 'agent', label: 'Agent' },
  { id: 'mcp', label: 'MCP 服务器' }, { id: 'permission', label: '权限' }, { id: 'instructions', label: '指令文件' },
  { id: 'plugin', label: '插件' }, { id: 'experimental', label: '实验特性' }, { id: 'unknown', label: '其他键' },
];
export const pageSectionId = (page: string, id: string) => `section:${page}:${encodeURIComponent(id)}`;
export const providerSectionId = (name: string) => `provider:${encodeURIComponent(name)}`;
