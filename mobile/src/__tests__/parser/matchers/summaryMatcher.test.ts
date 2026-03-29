import { buildSummaryBlock, isSummaryBlock, summaryMatcher } from '../../../parser/matchers/summaryMatcher'

describe('summaryMatcher', () => {
  it('detects Claude Code welcome summary blocks', () => {
    expect(isSummaryBlock([
      '▐▛███▜▌ Claude Code v2.1.81',
      '▝▜█████▛▘ Opus 4.6 (1M context) with high effort · API Usage Billing',
      '▘▘ ▝▝ X:\\WorkSpace',
    ], 'claudecode')).toBe(true)
  })

  it('rejects generic text blocks without welcome metadata', () => {
    expect(isSummaryBlock(['plain text', 'another line'], 'claudecode')).toBe(false)
    expect(isSummaryBlock(['Claude Code v2.1.81'], 'claudecode')).toBe(false)
  })

  it('builds summary blocks with title, version, subtitle, workdir and effort', () => {
    expect(buildSummaryBlock({
      appType: 'claudecode',
      lines: [
        '▐▛███▜▌ Claude Code v2.1.81',
        '▝▜█████▛▘ Opus 4.6 (1M context) with high effort · API Usage Billing',
        '▘▘ ▝▝ X:\\WorkSpace',
      ],
      raw: 'raw-summary',
      createdAt: 14,
    })).toEqual({
      id: 'summary-14',
      type: 'summary',
      appType: 'claudecode',
      raw: 'raw-summary',
      title: 'Claude Code',
      version: 'v2.1.81',
      subtitle: 'Opus 4.6 (1M context) with high effort · API Usage Billing',
      workDir: 'X:\\WorkSpace',
      effort: 'high effort',
      createdAt: 14,
    })
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'opencode' as const,
      lines: ['PS X:\\WorkSpace> opencode', 'OpenCode v1.2.3', 'gpt-5 with medium effort', '/workspace'],
      raw: 'PS X:\\WorkSpace> opencode\nOpenCode v1.2.3\ngpt-5 with medium effort\n/workspace',
      createdAt: 5,
    }
    expect(summaryMatcher.match(context)).toBe(true)
    expect(summaryMatcher.build(context).type).toBe('summary')
  })

  it('matches OpenCode summaries with shell prompts and lowercase titles', () => {
    expect(isSummaryBlock([
      'PS X:\\WorkSpace> opencode',
      '│ opencode v1.2.3',
      '│ gpt-5 with medium effort',
      '└ /workspace',
    ], 'opencode')).toBe(true)
  })
})
