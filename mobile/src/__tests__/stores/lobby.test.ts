import { beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
vi.mock('../../lib/api',async(importOriginal)=>({...await importOriginal<typeof import('../../lib/api')>(),listSessions:vi.fn(),stopSession:vi.fn(),getHostSummary:vi.fn()}))
import * as api from '../../lib/api'
import { useLobbyStore } from '../../stores/lobby'
import type { SessionSummary } from '../../lib/contract'
const session:SessionSummary={id:'s',title:'Test',cliType:'pi',state:'running',control:{state:'you'},lastActivityAt:'2026-11-11T00:00:00Z'}
beforeEach(()=>{setActivePinia(createPinia());vi.clearAllMocks()})
it('keeps a failed operation visible after a successful list refresh',async()=>{
 vi.mocked(api.stopSession).mockRejectedValue(new Error('connection failed'))
 vi.mocked(api.listSessions).mockResolvedValue([session])
 const store=useLobbyStore();expect(await store.runDangerousOperation('stop',session)).toBe(false)
 expect(store.operationError).not.toBeNull();await store.refreshSessions();expect(store.operationError).not.toBeNull()
 store.dismissOperationError();expect(store.operationError).toBeNull()
})
it('late list responses cannot restore an already removed session',async()=>{
 let resolve!:(value:SessionSummary[])=>void
 vi.mocked(api.listSessions).mockReturnValueOnce(new Promise(r=>{resolve=r})).mockResolvedValueOnce([])
 const store=useLobbyStore();const old=store.refreshSessions();await store.refreshSessions();resolve([session]);await old
 expect(store.sessions).toEqual([])
})
