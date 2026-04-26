export type AppType = 'claudecode' | 'opencode' | 'codex' | 'amagicode' | 'generic'

const APP_TYPE_ALIASES: Record<string, AppType> = {
  claude: 'claudecode',
  claudecode: 'claudecode',
  opencode: 'opencode',
  codex: 'codex',
  amagicode: 'amagicode',
}

export function resolveAppType(value?: string | null): AppType {
  if (!value) return 'generic'
  return APP_TYPE_ALIASES[value] ?? 'generic'
}

export function isLiquidTerminalCapable(appType: AppType): boolean {
  return appType === 'claudecode' || appType === 'opencode'
}
