const ANSI_PATTERN_SOURCE = '(?:\\u001B\\][^\\u0007]*(?:\\u0007|\\u001B\\\\))|(?:[\\u001B\\u009B][[\\]()#;?]*(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PR-TZcf-nq-uy=><~]))'

function createAnsiPattern() {
  return new RegExp(ANSI_PATTERN_SOURCE, 'g')
}

export function stripAnsi(value: string): string {
  return value.replace(createAnsiPattern(), '')
}

export function hasAnsi(value: string): boolean {
  return createAnsiPattern().test(value)
}
