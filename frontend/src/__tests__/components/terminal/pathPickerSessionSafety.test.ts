import { describe, expect, it } from 'vitest'
import { PathPickerModel, type PathLoader } from '../../../components/terminal/pathPickerModel'
import type { ListDirectoriesResult } from '../../../api/paths'

function listing(root: string, names: string[] = []): ListDirectoriesResult {
  return { root, parent: '/', dirs: names.map(name => ({ name, path: `${root}/${name}` })), truncated: false }
}

describe('session path picker stale-response protection', () => {
  it('reopening ignores an earlier directory response', async () => {
    const resolvers = new Map<string, (result: ListDirectoriesResult) => void>()
    const load: PathLoader = root => new Promise(resolve => resolvers.set(root, resolve))
    const model = new PathPickerModel(load)
    const old = model.resetAndLoad('/old/project')
    const current = model.resetAndLoad('/new/project')
    resolvers.get('/new')!(listing('/new', ['correct']))
    await current
    resolvers.get('/old')!(listing('/old', ['stale']))
    await old
    expect(model.listing?.root).toBe('/new')
    expect(model.loading).toBe(false)
    expect(model.rows.some(row => row.kind === 'dir' && row.name === 'stale')).toBe(false)
  })

  it('an old expanded-child response cannot replace the new cache', async () => {
    const resolvers: ((result: ListDirectoriesResult) => void)[] = []
    const load: PathLoader = async root => {
      if (root === '/root/child') return new Promise(resolve => resolvers.push(resolve))
      return listing('/root', ['child'])
    }
    const model = new PathPickerModel(load)
    await model.resetAndLoad('/root/project')
    const old = model.toggleExpand('/root/child')
    await model.resetAndLoad('/root/project')
    const current = model.toggleExpand('/root/child')
    resolvers[0]!(listing('/root/child', ['stale']))
    await old
    expect(model.rows.some(row => row.kind === 'dir' && row.name === 'stale')).toBe(false)
    expect(model.rows.some(row => row.kind === 'loading')).toBe(true)
    resolvers[1]!(listing('/root/child', ['current']))
    await current
    expect(model.rows.some(row => row.kind === 'dir' && row.name === 'current')).toBe(true)
  })
})
