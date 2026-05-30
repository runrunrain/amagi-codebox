import { describe, expect, it } from 'vitest'
import terminalPageSource from '../../views/TerminalPage.vue?raw'

function extractPointerFineMediaBlock(source: string): string {
  const mediaStart = source.indexOf('@media (hover: hover) and (pointer: fine)')
  expect(mediaStart).toBeGreaterThanOrEqual(0)

  const openingBrace = source.indexOf('{', mediaStart)
  expect(openingBrace).toBeGreaterThanOrEqual(0)

  let depth = 0
  for (let index = openingBrace; index < source.length; index += 1) {
    const char = source[index]
    if (char === '{') {
      depth += 1
    } else if (char === '}') {
      depth -= 1
      if (depth === 0) {
        return source.slice(openingBrace + 1, index)
      }
    }
  }

  throw new Error('Unable to find the end of the pointer-fine media block')
}

describe('TerminalPage mobile text mode visibility CSS', () => {
  it('does not let desktop pointer media hide the mobile composer or key tray', () => {
    // Arrange
    const pointerFineMediaBlock = extractPointerFineMediaBlock(terminalPageSource)
    const forbiddenDisplayNoneOverride = /\.(?:mobile-input-bar|shortcut-bar)\b[^{]*\{[^}]*display\s*:\s*none\s*;?/s

    // Act
    const containsForbiddenOverride = forbiddenDisplayNoneOverride.test(pointerFineMediaBlock)

    // Assert
    expect(containsForbiddenOverride).toBe(false)
  })

  it('uses viewport width as a mobile text mode guard for Chrome DevTools device emulation', () => {
    expect(terminalPageSource).toContain('MOBILE_TEXT_VIEWPORT_MAX_WIDTH')
    expect(terminalPageSource).toContain('window.visualViewport?.width')
    expect(terminalPageSource).toContain('viewportWidth <= MOBILE_TEXT_VIEWPORT_MAX_WIDTH')
    expect(terminalPageSource).toContain('!isNarrowViewport')
  })
})

describe('TerminalPage composer spacer layout', () => {
  it('uses a dynamic CSS variable for composer spacer on text-view padding-bottom', () => {
    // The .terminal-text-view should use --session-composer-spacer var for bottom padding
    expect(terminalPageSource).toContain('--session-composer-spacer')
    // The inline style binding should set the CSS variable from reactive state
    expect(terminalPageSource).toContain('composerSpacerVar')
  })

  it('has an input event handler on the textarea that updates spacer', () => {
    expect(terminalPageSource).toContain('onMobileInputChange')
    expect(terminalPageSource).toContain('@input="onMobileInputChange"')
  })

  it('provides a spacer measurement function that accounts for textarea growth', () => {
    expect(terminalPageSource).toContain('updateComposerSpacer')
    // Must clamp textarea measurement to max-height (160px)
    expect(terminalPageSource).toMatch(/Math\.min.*160/)
  })

  it('has a fallback spacer value of at least 156px (SessionTimeline default)', () => {
    // The default value should cover the maximum composer dock height:
    // textarea max 160 + controls 48 + dock padding 10 + breathing room > 156
    expect(terminalPageSource).toMatch(/156px/)
  })
})
