import { buildTranscriptBlocks, dedupeAdjacentSemanticBlocks, isTranscriptCommandLine, isTranscriptDividerLine, promoteTextBlocksToMarkdown, splitTranscriptSections } from '../../composables/useTerminalTranscript'

describe('buildTranscriptBlocks', () => {
  it('merges adjacent plain text lines into one text block', () => {
    const blocks = buildTranscriptBlocks(['hello', 'world'], 'claudecode', 10)

    expect(blocks).toEqual([
      {
        id: 'text-10',
        type: 'text',
        appType: 'claudecode',
        raw: 'hello\nworld',
        content: 'hello\nworld',
        createdAt: 10,
      },
    ])
  })

  it('extracts fenced code blocks as a dedicated code block', () => {
    const blocks = buildTranscriptBlocks([
      'intro',
      '```ts src/main.ts',
      'const x = 1',
      '```',
      'tail',
    ], 'opencode', 20)

    expect(blocks).toEqual([
      {
        id: 'text-20',
        type: 'text',
        appType: 'opencode',
        raw: 'intro',
        content: 'intro',
        createdAt: 20,
      },
      {
        id: 'code-21',
        type: 'code',
        appType: 'opencode',
        raw: '```ts src/main.ts\nconst x = 1\n```',
        code: 'const x = 1',
        language: 'ts',
        filename: 'src/main.ts',
        createdAt: 21,
      },
      {
        id: 'text-22',
        type: 'text',
        appType: 'opencode',
        raw: 'tail',
        content: 'tail',
        createdAt: 22,
      },
    ])
  })

  it('extracts status lines as status blocks', () => {
    const blocks = buildTranscriptBlocks([
      'Connected to session.',
      'working',
    ], 'claudecode', 30)

    expect(blocks[0]).toEqual({
      id: 'status-30',
      type: 'status',
      appType: 'claudecode',
      raw: 'Connected to session.',
      content: 'Connected to session.',
      createdAt: 30,
    })
    expect(blocks[1]).toEqual({
      id: 'text-31',
      type: 'text',
      appType: 'claudecode',
      raw: 'working',
      content: 'working',
      createdAt: 31,
    })
  })

  it('extracts multiline markdown sections through the default registry', () => {
    const blocks = buildTranscriptBlocks([
      '# Plan',
      '- first item',
      '- second item',
    ], 'opencode', 32)

    expect(blocks).toEqual([
      {
        id: 'markdown-32',
        type: 'markdown',
        appType: 'opencode',
        raw: '# Plan\n- first item\n- second item',
        content: '# Plan\n- first item\n- second item',
        createdAt: 32,
      },
    ])
  })

  it('preserves blank lines inside markdown sections so multi-paragraph markdown stays one block', () => {
    const blocks = buildTranscriptBlocks([
      '# Plan',
      '',
      'Paragraph text',
      '',
      '- item one',
      '- item two',
    ], 'claudecode', 33)

    expect(blocks).toEqual([
      {
        id: 'markdown-33',
        type: 'markdown',
        appType: 'claudecode',
        raw: '# Plan\n\nParagraph text\n\n- item one\n- item two',
        content: '# Plan\n\nParagraph text\n\n- item one\n- item two',
        createdAt: 33,
      },
    ])
  })

  it('keeps paragraph plus following list in one markdown section when separated by a blank line', () => {
    const sections = splitTranscriptSections([
      'Here is the plan:',
      '',
      '- first item',
      '- second item',
    ])

    expect(sections).toEqual([
      ['Here is the plan:', '', '- first item', '- second item'],
    ])
  })

  it('classifies paragraph plus list markdown as one markdown block', () => {
    const blocks = buildTranscriptBlocks([
      'Here is the plan:',
      '',
      '- first item',
      '- second item',
    ], 'opencode', 34)

    expect(blocks).toEqual([
      {
        id: 'markdown-34',
        type: 'markdown',
        appType: 'opencode',
        raw: 'Here is the plan:\n\n- first item\n- second item',
        content: 'Here is the plan:\n\n- first item\n- second item',
        createdAt: 34,
      },
    ])
  })

  it('classifies multi-line Claude narrative reply as a markdown block', () => {
    const blocks = buildTranscriptBlocks([
      '下面是方案：',
      '第一步先检查配置。',
      '第二步再执行迁移。',
    ], 'claudecode', 35)

    expect(blocks).toEqual([
      {
        id: 'markdown-35',
        type: 'markdown',
        appType: 'claudecode',
        raw: '下面是方案：\n第一步先检查配置。\n第二步再执行迁移。',
        content: '下面是方案：\n第一步先检查配置。\n第二步再执行迁移。',
        createdAt: 35,
      },
    ])
  })

  it('promotes eligible multiline text blocks to markdown for Claude/OpenCode', () => {
    const blocks = promoteTextBlocksToMarkdown([
      {
        id: 'text-1',
        type: 'text',
        appType: 'claudecode',
        raw: '第一步先检查配置。\n第二步再执行迁移。',
        content: '第一步先检查配置。\n第二步再执行迁移。',
        createdAt: 1,
      },
    ], 'claudecode')

    expect(blocks).toEqual([
      {
        id: 'text-1',
        type: 'markdown',
        appType: 'claudecode',
        raw: '第一步先检查配置。\n第二步再执行迁移。',
        content: '第一步先检查配置。\n第二步再执行迁移。',
        createdAt: 1,
      },
    ])
  })

  it('does not promote semantic or shell-like text blocks to markdown', () => {
    const blocks = promoteTextBlocksToMarkdown([
      {
        id: 'text-1',
        type: 'text',
        appType: 'claudecode',
        raw: 'PS X:\\WorkSpace> claude\noutput',
        content: 'PS X:\\WorkSpace> claude\noutput',
        createdAt: 1,
      },
      {
        id: 'prompt-2',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "how do I log an error?"',
        content: '> Try "how do I log an error?"',
        primaryAction: 'how do I log an error?',
        createdAt: 2,
      },
    ], 'claudecode')

    expect(blocks).toEqual([
      {
        id: 'text-1',
        type: 'text',
        appType: 'claudecode',
        raw: 'PS X:\\WorkSpace> claude\noutput',
        content: 'PS X:\\WorkSpace> claude\noutput',
        createdAt: 1,
      },
      {
        id: 'prompt-2',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "how do I log an error?"',
        content: '> Try "how do I log an error?"',
        primaryAction: 'how do I log an error?',
        createdAt: 2,
      },
    ])
  })

  it('recognizes divider lines used by terminal UIs', () => {
    expect(isTranscriptDividerLine('────────────────────────────')).toBe(true)
    expect(isTranscriptDividerLine('-----------')).toBe(false)
    expect(isTranscriptDividerLine('plain text')).toBe(false)
  })

  it('recognizes command-style prompt lines as section boundaries', () => {
    expect(isTranscriptCommandLine('> /load-session')).toBe(true)
    expect(isTranscriptCommandLine('   > 你好')).toBe(true)
    expect(isTranscriptCommandLine('PS X:\\WorkSpace> claude')).toBe(true)
    expect(isTranscriptCommandLine('$ opencode')).toBe(true)
    expect(isTranscriptCommandLine('/workspace> opencode')).toBe(true)
    expect(isTranscriptCommandLine('plain text')).toBe(false)
  })

  it('splits transcript sections on divider lines and blank lines', () => {
    const sections = splitTranscriptSections([
      'Claude Code v2.1.81',
      'X:\\WorkSpace',
      '────────────────────────────',
      '> Try "how do I log an error?"',
      '',
      '⏵⏵ bypass permissions on',
    ])

    expect(sections).toEqual([
      ['Claude Code v2.1.81', 'X:\\WorkSpace'],
      ['> Try "how do I log an error?"'],
      ['⏵⏵ bypass permissions on'],
    ])
  })

  it('does not split blank lines inside markdown-like sections', () => {
    const sections = splitTranscriptSections([
      '# Plan',
      '',
      'Paragraph text',
      '',
      '- item one',
      '- item two',
    ])

    expect(sections).toEqual([
      ['# Plan', '', 'Paragraph text', '', '- item one', '- item two'],
    ])
  })

  it('splits shell command lines into their own sections', () => {
    const sections = splitTranscriptSections([
      'Claude Code v2.1.81',
      'X:\\WorkSpace',
      '> /load-session',
      'result line',
    ])

    expect(sections).toEqual([
      ['Claude Code v2.1.81', 'X:\\WorkSpace'],
      ['> /load-session'],
      ['result line'],
    ])
  })

  it('keeps fenced code blocks intact while splitting other sections', () => {
    const sections = splitTranscriptSections([
      'intro',
      '```ts',
      'const x = 1',
      '────────────────',
      '```',
      'tail',
    ])

    expect(sections).toEqual([
      ['intro'],
      ['```ts', 'const x = 1', '────────────────', '```'],
      ['tail'],
    ])
  })

  it('produces multiple blocks for Claude-like intro content separated by dividers', () => {
    const blocks = buildTranscriptBlocks([
      'Claude Code v2.1.81',
      'Opus 4.6 (1M context) with high effort',
      'X:\\WorkSpace',
      '────────────────────────────',
      '> Try "how do I log an error?"',
      '────────────────────────────',
      '⏵⏵ bypass permissions on',
    ], 'claudecode', 40)

    expect(blocks).toEqual([
      {
        id: 'summary-40',
        type: 'summary',
        appType: 'claudecode',
        raw: 'Claude Code v2.1.81\nOpus 4.6 (1M context) with high effort\nX:\\WorkSpace',
        title: 'Claude Code',
        version: 'v2.1.81',
        subtitle: 'Opus 4.6 (1M context) with high effort',
        workDir: 'X:\\WorkSpace',
        effort: 'high effort',
        createdAt: 40,
      },
      {
        id: 'prompt-41',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "how do I log an error?"',
        content: '> Try "how do I log an error?"',
        primaryAction: 'how do I log an error?',
        createdAt: 41,
      },
      {
        id: 'action-42',
        type: 'action',
        appType: 'claudecode',
        raw: '⏵⏵ bypass permissions on',
        content: '⏵⏵ bypass permissions on',
        shortcutHint: undefined,
        createdAt: 42,
      },
    ])
  })

  it('dedupes adjacent repeated semantic blocks', () => {
    const blocks = dedupeAdjacentSemanticBlocks([
      {
        id: 'prompt-1',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "hello"',
        content: '> Try "hello"',
        primaryAction: 'hello',
        createdAt: 1,
      },
      {
        id: 'prompt-2',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "hello"',
        content: '> Try "hello"',
        primaryAction: 'hello',
        createdAt: 2,
      },
      {
        id: 'action-3',
        type: 'action',
        appType: 'claudecode',
        raw: '⏵⏵ bypass permissions on',
        content: '⏵⏵ bypass permissions on',
        shortcutHint: '(shift+tab to cycle)',
        createdAt: 3,
      },
      {
        id: 'action-4',
        type: 'action',
        appType: 'claudecode',
        raw: '⏵⏵ bypass permissions on',
        content: '⏵⏵ bypass permissions on',
        shortcutHint: '(shift+tab to cycle)',
        createdAt: 4,
      },
    ])

    expect(blocks).toEqual([
      {
        id: 'prompt-1',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "hello"',
        content: '> Try "hello"',
        primaryAction: 'hello',
        createdAt: 1,
      },
      {
        id: 'action-3',
        type: 'action',
        appType: 'claudecode',
        raw: '⏵⏵ bypass permissions on',
        content: '⏵⏵ bypass permissions on',
        shortcutHint: '(shift+tab to cycle)',
        createdAt: 3,
      },
    ])
  })

  it('keeps non-adjacent or distinct blocks intact', () => {
    const blocks = dedupeAdjacentSemanticBlocks([
      {
        id: 'prompt-1',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "hello"',
        content: '> Try "hello"',
        primaryAction: 'hello',
        createdAt: 1,
      },
      {
        id: 'text-2',
        type: 'text',
        appType: 'claudecode',
        raw: 'plain',
        content: 'plain',
        createdAt: 2,
      },
      {
        id: 'prompt-3',
        type: 'prompt',
        appType: 'claudecode',
        raw: '> Try "hello"',
        content: '> Try "hello"',
        primaryAction: 'hello',
        createdAt: 3,
      },
    ])

    expect(blocks).toHaveLength(3)
  })

  it('keeps tool line and immediate continuation output in one section', () => {
    const sections = splitTranscriptSections([
      'Read 3 files (ctrl+o to expand)',
      '  src/main.ts',
      '  src/app.ts',
      '',
      'Next paragraph',
    ])

    expect(sections).toEqual([
      ['Read 3 files (ctrl+o to expand)', '  src/main.ts', '  src/app.ts'],
      ['Next paragraph'],
    ])
  })

  it('builds a richer tool block when continuation lines follow the tool title', () => {
    const blocks = buildTranscriptBlocks([
      'Read 3 files (ctrl+o to expand)',
      '  src/main.ts',
      '  src/app.ts',
    ], 'claudecode', 50)

    expect(blocks).toEqual([
      {
        id: 'tool-50',
        type: 'tool',
        appType: 'claudecode',
        raw: 'Read 3 files (ctrl+o to expand)\n  src/main.ts\n  src/app.ts',
        toolName: 'Read',
        title: 'Read 3 files (ctrl+o to expand)',
        summary: 'src/main.ts\nsrc/app.ts',
        shortcutHint: '(ctrl+o to expand)',
        createdAt: 50,
      },
    ])
  })

  it('keeps OpenCode snake_case tool lines with tree-style output in one section', () => {
    const sections = splitTranscriptSections([
      'read_file src/main.ts',
      '│ src/main.ts',
      '└ 1 file read',
      'Next paragraph',
    ])

    expect(sections).toEqual([
      ['read_file src/main.ts', '│ src/main.ts', '└ 1 file read'],
      ['Next paragraph'],
    ])
  })

  it('does not promote OpenCode tool transcripts to markdown', () => {
    const blocks = promoteTextBlocksToMarkdown([
      {
        id: 'text-3',
        type: 'text',
        appType: 'opencode',
        raw: 'read_file src/main.ts\n1 file read',
        content: 'read_file src/main.ts\n1 file read',
        createdAt: 3,
      },
    ], 'opencode')

    expect(blocks).toEqual([
      {
        id: 'text-3',
        type: 'text',
        appType: 'opencode',
        raw: 'read_file src/main.ts\n1 file read',
        content: 'read_file src/main.ts\n1 file read',
        createdAt: 3,
      },
    ])
  })

  it('detects TODO blocks from checkbox lines', () => {
    const blocks = buildTranscriptBlocks(['- [ ] task1', '- [x] task2'], 'claudecode', 60)

    expect(blocks[0].type).toBe('todo')
  })

  it('detects table blocks from pipe syntax', () => {
    const blocks = buildTranscriptBlocks(['| A | B |', '| --- | --- |', '| 1 | 2 |'], 'opencode', 61)

    expect(blocks[0].type).toBe('table')
  })

  it('separates TODO from surrounding text', () => {
    const blocks = buildTranscriptBlocks([
      'intro',
      '',
      '- [ ] task1',
      '- [x] task2',
      '',
      'outro',
    ], 'claudecode', 62)

    expect(blocks).toHaveLength(3)
    expect(blocks[0].type).toBe('text')
    expect(blocks[1].type).toBe('todo')
    expect(blocks[2].type).toBe('text')
  })

})
