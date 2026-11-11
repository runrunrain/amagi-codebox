import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }))
vi.mock('../../api/remoteClient', () => ({
  listRemoteHosts: vi.fn(async () => []), connectRemoteHost: vi.fn(async () => {}), disconnectRemoteHost: vi.fn(async () => {}),
  listRemoteSessions: vi.fn(async () => []), getRemoteProviderJSON: vi.fn(), getRemoteSettingsJSON: vi.fn(),
  remoteErrorText: String, stopRemoteSession: vi.fn(),
}))
import * as api from '../../api/remoteClient'
import { EventsOn } from '../../../wailsjs/runtime/runtime'
import { useRemoteClientStore } from '../../stores/remoteClient'
function deferred<T>() { let resolve!: (value:T)=>void; const promise=new Promise<T>(r=>{resolve=r}); return {promise,resolve} }
beforeEach(()=>{setActivePinia(createPinia());vi.useFakeTimers();vi.clearAllMocks()})
afterEach(()=>vi.useRealTimers())
it('returning local during connect never re-enters remote and disconnects the completed connection',async()=>{
 const d=deferred<Awaited<ReturnType<typeof api.connectRemoteHost>>>();vi.mocked(api.connectRemoteHost).mockReturnValueOnce(d.promise)
 const s=useRemoteClientStore();const pending=s.switchToHost('a');await Promise.resolve()
 const local=s.switchToLocal();d.resolve({} as Awaited<ReturnType<typeof api.connectRemoteHost>>);await Promise.all([pending,local])
 expect(s.scope).toBe('local');expect(s.connectState).toBe('disconnected');expect(api.disconnectRemoteHost).toHaveBeenCalledWith('a')
})
it('ignores a late session list after switching local',async()=>{
 const s=useRemoteClientStore();await s.switchToHost('a')
 const d=deferred<any[]>();vi.mocked(api.listRemoteSessions).mockReturnValueOnce(d.promise)
 const pending=s.refreshRemoteSessions();await s.switchToLocal();d.resolve([{id:'old-host'}]);await pending
 expect(s.remoteSessions).toEqual([]);expect(s.lastSyncedAt).toBeNull()
})
it('latest provider selection wins and close cannot be undone by a late response',async()=>{
 const s=useRemoteClientStore();await s.switchToHost('a')
 const old=deferred<string>();vi.mocked(api.getRemoteProviderJSON).mockReturnValueOnce(old.promise).mockResolvedValueOnce('new')
 const pending=s.openRemoteProvider('old');await s.openRemoteProvider('new');old.resolve('old');await pending
 expect(s.remoteProviderDoc).toBe('new')
 const closed=deferred<string>();vi.mocked(api.getRemoteProviderJSON).mockReturnValueOnce(closed.promise)
 const opening=s.openRemoteProvider('closed');s.closeRemoteProvider();closed.resolve('late');await opening
 expect(s.remoteProviderDoc).toBe('');expect(s.remoteProviderName).toBe('')
 await s.switchToLocal()
})
it('does not apply settings from a previous host',async()=>{
 const s=useRemoteClientStore();await s.switchToHost('a')
 const d=deferred<string>();vi.mocked(api.getRemoteSettingsJSON).mockReturnValueOnce(d.promise)
 const pending=s.loadRemoteSettings();await s.switchToHost('b');d.resolve('host-a-settings');await pending
 expect(s.remoteSettingsDoc).toBe('');await s.switchToLocal()
})
it('blocks session actions while the host is still connecting',async()=>{
 const d=deferred<Awaited<ReturnType<typeof api.connectRemoteHost>>>();vi.mocked(api.connectRemoteHost).mockReturnValueOnce(d.promise)
 const s=useRemoteClientStore();const pending=s.switchToHost('a');await Promise.resolve()
 await expect(s.stopRemoteSession('session')).rejects.toThrow('尚未连接');expect(api.stopRemoteSession).not.toHaveBeenCalled()
 d.resolve({} as Awaited<ReturnType<typeof api.connectRemoteHost>>);await pending;await s.switchToLocal()
})

it('ignores terminal events emitted by a previously selected host',async()=>{
 const s=useRemoteClientStore();await s.switchToHost('b')
 const callback=vi.mocked(EventsOn).mock.calls.find(call=>call[0]==='rc:control-state')![1]
 callback({hostId:'a',sessionId:'same-id',state:'you'})
 expect(s.remoteTerminalStates['same-id']).toBeUndefined()
 callback({hostId:'b',sessionId:'same-id',state:'other'})
 expect(s.remoteTerminalStates['same-id']?.controlState).toBe('other')
 await s.switchToLocal()
})
