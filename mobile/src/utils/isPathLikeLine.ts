const PATH_LIKE_PATTERN = /^(?:[A-Za-z]:\\|\\\\|\/|\.\/|\.\.\/).+/i

export function isPathLikeLine(line: string): boolean {
  return PATH_LIKE_PATTERN.test(line.trim())
}
