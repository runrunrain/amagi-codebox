import { ref, computed } from 'vue'

/**
 * Shell descriptor from backend capability truth source.
 */
export interface ShellDescriptor {
  key: string
  label: string
  resolvedPath: string
  available: boolean
  isDefault: boolean
}

/**
 * Full platform capabilities -- mirrors Go PlatformCapabilities struct.
 * Single source of truth for UI rendering decisions.
 */
export interface PlatformCapabilities {
  platformId: string
  os: string
  arch: string
  embeddedTerminalSupported: boolean
  standaloneTerminalSupported: boolean
  systemTraySupported: boolean
  fileOpenSupported: boolean
  updateCheckSupported: boolean
  updateInstallSupported: boolean
  autoStartSupported: boolean
  singleInstanceSupported: boolean
  windowActivationSupported: boolean
  hideOnCloseSupported: boolean
  backgroundResidentSupported: boolean
  closeAction: string
  secureSecretStoreKind: string
  pathDiagnosticsSupported: boolean
  supportedShells: ShellDescriptor[]
  defaultShellKey: string
}

// Module-level singleton: capabilities never change during app lifetime.
const cached = ref<PlatformCapabilities | null>(null)

async function fetchCapabilities(): Promise<PlatformCapabilities> {
  return (window as any)['go']['main']['App']['GetPlatformCapabilities']()
}

export function usePlatformCapabilities() {
  /**
   * Fetch capabilities from backend exactly once. Call this early (e.g. onMounted)
   * before any code that depends on shell/mode defaults.
   */
  async function ensure(): Promise<PlatformCapabilities | null> {
    if (cached.value) return cached.value
    try {
      cached.value = await fetchCapabilities()
      return cached.value
    } catch (err) {
      console.error('Failed to load platform capabilities:', err)
      return null
    }
  }

  /** Raw capabilities object (null until loaded). */
  const caps = computed(() => cached.value)

  /** true when running on Windows. */
  const isWindows = computed(() => cached.value?.os === 'windows')

  /** true when running on macOS. */
  const isDarwin = computed(() => cached.value?.os === 'darwin')

  /**
   * Built-in shell options derived from backend SupportedShells.
   * Each entry has: value (key), label, resolvedPath, available, isDefault.
   */
  const builtinShellOptions = computed(() => {
    if (!cached.value) return []
    return cached.value.supportedShells.map(s => ({
      value: s.key,
      label: s.label,
      resolvedPath: s.resolvedPath,
      available: s.available,
      isDefault: s.isDefault,
    }))
  })

  /** Default shell key for the current platform (e.g. 'pwsh' on Windows, 'zsh' on macOS). */
  const defaultShellKey = computed(() => cached.value?.defaultShellKey || '')

  /**
   * Available launch modes filtered by platform capability.
   * On macOS, 'terminal' (standalone) is excluded.
   */
  const launchModes = computed(() => {
    // Icon field is optional visual hint; plain characters work fine
    const modes = [{ value: 'embedded', label: '内嵌终端', icon: '▨' }]
    if (cached.value?.standaloneTerminalSupported) {
      modes.push({ value: 'terminal', label: '独立窗口', icon: '◆' })
    }
    return modes
  })

  /**
   * Resolve a shell key to its executable path using backend-provided data.
   * Falls back to the key itself (for user-saved custom paths).
   */
  function resolveShellPath(shellKey: string, customPath: string): string {
    if (shellKey === '') return ''
    if (shellKey === '__custom__') return customPath
    if (!cached.value) return shellKey
    const descriptor = cached.value.supportedShells.find(s => s.key === shellKey)
    return descriptor?.resolvedPath || shellKey
  }

  return {
    caps,
    isWindows,
    isDarwin,
    builtinShellOptions,
    defaultShellKey,
    launchModes,
    resolveShellPath,
    ensure,
  }
}
