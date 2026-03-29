import { stripTuiChars } from '../../utils/stripTuiChars'

describe('stripTuiChars', () => {
  it('strips Bubble Tea box drawing and block decoration while preserving text', () => {
    expect(stripTuiChars('│ read_file src/main.ts ──')).toBe('  read_file src/main.ts   ')
    expect(stripTuiChars('┌────────────────┐')).toBe('                  ')
    expect(stripTuiChars('█ OpenCode ▓')).toBe('  OpenCode  ')
  })
})
