import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { isReadableLegacyText, normalizeTranscriptChunk, resetTranscriptNormalizerState } from '../../utils/transcriptNormalizer'

describe('isReadableLegacyText', () => {
  it('returns true for natural language with action verbs', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('Build completed successfully. 3 files changed.')).toBe(true)
    expect(isReadableLegacyText('Error: file not found')).toBe(true)
    expect(isReadableLegacyText('Successfully installed 5 packages')).toBe(true)
    expect(isReadableLegacyText('npm WARN deprecated package')).toBe(true)
  })

  it('returns true for readable markdown patterns', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('# Heading text')).toBe(true)
    expect(isReadableLegacyText('- list item one')).toBe(true)
    expect(isReadableLegacyText('1. ordered list item')).toBe(true)
    expect(isReadableLegacyText('[link text](https://example.com)')).toBe(true)
    expect(isReadableLegacyText('**bold text**')).toBe(true)
  })

  it('returns false for blockquote-like text (matches TUI hint pattern)', () => {
    // Arrange & Act & Assert
    // '> ' prefix matches TUI hint pattern /^[❯›>]\s*\S/, so blockquotes are conservatively filtered
    expect(isReadableLegacyText('> blockquote content')).toBe(false)
  })

  it('returns true for text with 3+ chars containing unicode letters', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('hello world')).toBe(true)
    expect(isReadableLegacyText('short')).toBe(true)
  })

  it('returns false for empty or whitespace-only text', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('')).toBe(false)
    expect(isReadableLegacyText('   ')).toBe(false)
    expect(isReadableLegacyText('\n\n')).toBe(false)
  })

  it('returns false for standalone numeric values', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('20')).toBe(false)
    expect(isReadableLegacyText('12345')).toBe(false)
    expect(isReadableLegacyText('42.5')).toBe(false)
  })

  it('returns false for numeric-only patterns with punctuation', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('100%')).toBe(false)
    expect(isReadableLegacyText('1,234')).toBe(false)
    expect(isReadableLegacyText('1/3')).toBe(false)
  })

  it('returns false for spinner-like patterns', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('|')).toBe(false)
    expect(isReadableLegacyText('/')).toBe(false)
    expect(isReadableLegacyText('-')).toBe(false)
    expect(isReadableLegacyText('\\')).toBe(false)
    expect(isReadableLegacyText('...')).toBe(false)
  })

  it('returns false for transient status text patterns', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('thinking')).toBe(false)
    expect(isReadableLegacyText('Thinking...')).toBe(false)
    expect(isReadableLegacyText('processing')).toBe(false)
    expect(isReadableLegacyText('loading')).toBe(false)
  })

  it('returns false for TUI menu and hint text', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('press enter to continue')).toBe(false)
    expect(isReadableLegacyText('ESC to close')).toBe(false)
  })

  it('returns false for JSON/object-like payloads', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('{"key":"value"}')).toBe(false)
    expect(isReadableLegacyText('[1,2,3]')).toBe(false)
    expect(isReadableLegacyText('[object Object]')).toBe(false)
  })

  it('returns false for text containing ANSI escape sequences', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('\u001B[32mgreen text\u001B[0m')).toBe(false)
  })

  it('returns false for text containing TUI box-drawing characters', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('\u2560 Settings \u2563')).toBe(false)
    expect(isReadableLegacyText('\u2500\u2500\u2500')).toBe(false)
  })

  it('returns false for progress bar patterns', () => {
    // Arrange & Act & Assert
    expect(isReadableLegacyText('Building...45')).toBe(false)
    expect(isReadableLegacyText('Installing modules...123')).toBe(false)
  })
})

describe('normalizeTranscriptChunk (diagnostic isolation)', () => {
  beforeEach(() => {
    resetTranscriptNormalizerState()
  })

  afterEach(() => {
    resetTranscriptNormalizerState()
  })

  it('produces cleanText and no diagnostics for plain text', () => {
    // Arrange
    const chunk = 'Hello world'

    // Act
    const result = normalizeTranscriptChunk(chunk)

    // Assert
    expect(result.cleanText).toBe('Hello world')
    expect(result.diagnostics).toHaveLength(0)
  })

  it('isolates JSON object payload into diagnostics with empty cleanText', () => {
    // Arrange
    const chunk = JSON.stringify({ token: 'sk-1234567890abcdef', nested: { key: 'value' } })

    // Act
    const result = normalizeTranscriptChunk(chunk)

    // Assert
    expect(result.cleanText).toBe('')
    expect(result.diagnostics).toHaveLength(1)
    expect(result.diagnostics[0].reason).toBe('object-payload')
  })

  it('keeps fenced JSON markdown as cleanText (not isolated)', () => {
    // Arrange
    const chunk = '```json\n{"key":"value"}\n```'

    // Act
    const result = normalizeTranscriptChunk(chunk)

    // Assert - fenced code block should not be treated as object payload
    expect(result.diagnostics.some((d) => d.reason === 'object-payload')).toBe(false)
    expect(result.cleanText.length).toBeGreaterThan(0)
  })

  it('reports diagnostic for ANSI escape sequences but keeps cleanText', () => {
    // Arrange
    const chunk = '\u001B[32mgreen\u001B[0m text'

    // Act
    const result = normalizeTranscriptChunk(chunk)

    // Assert
    expect(result.cleanText).toBe('green text')
    expect(result.diagnostics.some((d) => d.reason === 'ansi')).toBe(true)
  })

  it('reports diagnostic for TUI decoration characters', () => {
    // Arrange
    const chunk = '\u2500\u2500 Settings \u2500\u2500'

    // Act
    const result = normalizeTranscriptChunk(chunk)

    // Assert
    expect(result.diagnostics.some((d) => d.reason === 'tui')).toBe(true)
  })
})
