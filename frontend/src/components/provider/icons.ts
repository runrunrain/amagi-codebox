/**
 * provider 模块共享的内联 SVG 图标库（禁 emoji）
 *
 * 抽取自 ConfigCategoryCard.vue 与 4 个 *MapEditor.vue，消除 5 处重复定义。
 * 每个图标为 24x24 viewBox 的描边图标，stroke=currentColor 便于继承色相。
 *
 * 使用方式（v-html 注入组件内硬编码字符串，无外部输入路径，无 XSS 风险）：
 *   import { ICONS, getIcon } from './icons';
 *   const AGENT_ICON = ICONS.agent;        // 直接取
 *   const icon = getIcon('agent');         // 兜底 unknown
 *
 * 图标语义：
 * - model: 芯片（顶层模型字段）
 * - provider: 云（服务提供方）
 * - agent: 机器人头（智能体）
 * - mcp: 插块拼装（MCP servers）
 * - permission: 盾牌+锁（权限）
 * - instructions: 文档列表（说明文件）
 * - plugin: 积木拼块（插件）
 * - experimental: 烧瓶（实验性）
 * - unknown: 三点堆叠（兜底）
 */
export const ICONS: Readonly<Record<string, string>> = {
  model: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <rect x="7" y="7" width="10" height="10" rx="1.5"/>
      <rect x="10" y="10" width="4" height="4" rx="0.5" fill="currentColor" stroke="none"/>
      <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.5 5.5l1.4 1.4M17.1 17.1l1.4 1.4M5.5 18.5l1.4-1.4M17.1 6.9l1.4-1.4"/>
    </svg>`,
  provider: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M6 15a4 4 0 0 1 .8-7.9 5.5 5.5 0 0 1 10.6 1.4A3.75 3.75 0 0 1 17.5 15"/>
      <path d="M9 13.5l3-3 3 3"/>
      <path d="M12 10.5V18"/>
    </svg>`,
  agent: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <rect x="5" y="8" width="14" height="11" rx="3"/>
      <path d="M12 4v4"/>
      <circle cx="12" cy="3.5" r="1" fill="currentColor" stroke="none"/>
      <circle cx="9.5" cy="13" r="1.1" fill="currentColor" stroke="none"/>
      <circle cx="14.5" cy="13" r="1.1" fill="currentColor" stroke="none"/>
      <path d="M9.8 16h4.4"/>
    </svg>`,
  mcp: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M9 4v3.5L5 9.5V14"/>
      <path d="M15 4v3.5l4 2V14"/>
      <path d="M9 20v-3.5l-4-2"/>
      <path d="M15 20v-3.5l4-2"/>
      <rect x="9" y="3" width="6" height="2.4" rx="0.6"/>
      <rect x="3.6" y="8.4" width="2.8" height="2.4" rx="0.6" transform="rotate(-90 5 9.6)"/>
      <rect x="17.6" y="8.4" width="2.8" height="2.4" rx="0.6" transform="rotate(-90 19 9.6)"/>
    </svg>`,
  permission: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M12 3l7 3v5c0 4.2-2.8 7.8-7 9-4.2-1.2-7-4.8-7-9V6l7-3z"/>
      <rect x="9.5" y="10.5" width="5" height="3.6" rx="0.6"/>
      <path d="M10.4 10.5V9.2a1.6 1.6 0 0 1 3.2 0v1.3"/>
    </svg>`,
  instructions: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M6 3h9l3 3v15a0 0 0 0 1 0 0H6a0 0 0 0 1 0 0V3z"/>
      <path d="M14.5 3v3.5H18"/>
      <path d="M8.5 12h7M8.5 15h7M8.5 18h4"/>
    </svg>`,
  plugin: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M9 4.5h6v4a3 3 0 0 1-6 0v-4z"/>
      <path d="M12 4.5V2.5"/>
      <path d="M9 11v9a1 1 0 0 0 1 1h4a1 1 0 0 0 1-1v-9"/>
      <path d="M7 14H9M15 14h2"/>
    </svg>`,
  experimental: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M9 3h6"/>
      <path d="M10 3v5.5L5.5 17a2 2 0 0 0 1.8 3h9.4a2 2 0 0 0 1.8-3L14 8.5V3"/>
      <path d="M7.5 14h9"/>
      <circle cx="10" cy="17" r="0.8" fill="currentColor" stroke="none"/>
    </svg>`,
  unknown: `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="6" cy="12" r="1.4"/>
      <circle cx="12" cy="12" r="1.4"/>
      <circle cx="18" cy="12" r="1.4"/>
    </svg>`,
};

/**
 * 按 category 取图标，未命中返回 unknown 兜底。
 * 与原 ConfigCategoryCard 的 `ICONS[props.category] ?? ICONS.unknown` 行为完全一致。
 */
export function getIcon(category: string | undefined | null): string {
  if (!category) return ICONS.unknown;
  return ICONS[category] ?? ICONS.unknown;
}

/**
 * 各类的语义强调色（HIG 风格低饱和点缀色）
 * 用于略缩图背景的弱色调，前景仍是白色卡片。
 * 与 ConfigCategoryCard 原内联 ACCENTS 表保持完全一致，避免改色后视觉漂移。
 */
export const ACCENTS: Readonly<Record<string, string>> = {
  model: '#5856D6',        // indigo
  provider: '#007AFF',     // systemBlue
  agent: '#34C759',        // systemGreen
  mcp: '#FF9500',          // systemOrange
  permission: '#FF3B30',   // systemRed
  instructions: '#AF52DE', // systemPurple
  plugin: '#00C7BE',       // systemTeal
  experimental: '#FF2D55', // systemPink
  unknown: '#8E8E93',      // systemGray
};

export function getAccent(category: string | undefined | null): string {
  if (!category) return ACCENTS.unknown;
  return ACCENTS[category] ?? ACCENTS.unknown;
}
