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
