import { buildTodoBlock, isTodoBlock, todoMatcher } from '../../../parser/matchers/todoMatcher'

describe('todoMatcher', () => {
  it('detects unchecked todo lines', () => {
    expect(isTodoBlock(['- [ ] task one'])).toBe(true)
  })

  it('detects checked todo lines', () => {
    expect(isTodoBlock(['* [x] done task'])).toBe(true)
  })

  it('detects mixed todo lines', () => {
    expect(isTodoBlock(['intro', '- [ ] task one', '+ [X] task two'])).toBe(true)
  })

  it('rejects plain list items', () => {
    expect(isTodoBlock(['- item one', '* item two'])).toBe(false)
  })

  it('rejects plain text', () => {
    expect(isTodoBlock(['hello world', 'plain text'])).toBe(false)
  })

  it('builds todo blocks with correct items', () => {
    expect(buildTodoBlock({
      appType: 'opencode',
      lines: ['todo list', '- [ ] first task', 'note', '- [x] second task'],
      raw: 'todo list\n- [ ] first task\nnote\n- [x] second task',
      createdAt: 18,
    })).toEqual({
      id: 'todo-18',
      type: 'todo',
      appType: 'opencode',
      raw: 'todo list\n- [ ] first task\nnote\n- [x] second task',
      items: [
        { text: 'first task', completed: false },
        { text: 'second task', completed: true },
      ],
      content: 'todo list\n- [ ] first task\nnote\n- [x] second task',
      createdAt: 18,
    })
  })

  it('treats uppercase X as completed', () => {
    expect(buildTodoBlock({
      appType: 'claudecode',
      lines: ['- [X] shipped'],
      raw: '- [X] shipped',
      createdAt: 19,
    }).items).toEqual([
      { text: 'shipped', completed: true },
    ])
  })

  it('matcher follows registry contract', () => {
    const context = {
      appType: 'claudecode' as const,
      lines: ['- [ ] task one', '- [x] task two'],
      raw: '- [ ] task one\n- [x] task two',
      createdAt: 9,
    }
    expect(todoMatcher.match(context)).toBe(true)
    expect(todoMatcher.build(context).type).toBe('todo')
  })
})
