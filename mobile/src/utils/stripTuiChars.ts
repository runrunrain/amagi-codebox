import { stripAnsi } from './stripAnsi'

const TUI_DECORATION_PATTERN = /[─│┌┐└┘├┤┬┴┼━┃┏┓┗┛┣┫┳┻╋╔╗╚╝╠╣╦╩╬╭╮╰╯▁▂▃▄▅▆▇█▀▐▌░▒▓]/gu
const BRAILLE_SPINNER_PATTERN = /[\u2800-\u28FF]/gu
const ZERO_WIDTH_PATTERN = /[\u200B-\u200D\uFEFF]/gu

export function stripTuiChars(text: string): string {
  return stripAnsi(text)
    .replace(TUI_DECORATION_PATTERN, ' ')
    .replace(BRAILLE_SPINNER_PATTERN, ' ')
    .replace(ZERO_WIDTH_PATTERN, '')
}
