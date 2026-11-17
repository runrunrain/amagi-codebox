import { expect, it, vi } from 'vitest'
const mocks=vi.hoisted(()=>({store:{scope:'a',isRemoteMode:true,connectState:'connected',connectRevoked:false,subscribeRemoteSessionState:vi.fn()},send:vi.fn()}))
vi.mock('../../stores/remoteClient',()=>({useRemoteClientStore:()=>mocks.store}))
vi.mock('../../api/remoteClient',()=>({sendRemoteTerminalInput:mocks.send,resizeRemoteTerminal:vi.fn()}))
vi.mock('../../composables/useTerminalEngine',()=>({uint8ToBase64:vi.fn()}))
import { createRemoteTerminalTransport } from '../../composables/remoteTerminalTransport'
it('does not send old terminal input to a different host',async()=>{
 mocks.store.scope='a';mocks.send.mockClear()
 const transport=createRemoteTerminalTransport('same-session-id');mocks.store.scope='b'
 await expect(transport.write('rm something')).rejects.toThrow('输入未发送');expect(mocks.send).not.toHaveBeenCalled()
})
it('reports exit again after a session restarts',()=>{
 mocks.store.scope='a';mocks.store.subscribeRemoteSessionState.mockClear()
 const transport=createRemoteTerminalTransport('s');const exited=vi.fn();transport.subscribeExit(exited)
 const receive=mocks.store.subscribeRemoteSessionState.mock.calls[0]![1]
 receive({state:'exited'});receive({state:'exited'});expect(exited).toHaveBeenCalledTimes(1)
 receive({state:'running',restartBoundary:true});receive({state:'exited'});expect(exited).toHaveBeenCalledTimes(2)
})
