import {
  GetAIProxyState, SaveAIProxySettings, StartAIProxy, StopAIProxy,
  SyncAIProxyV2rayN, ImportAIProxySubscription, RefreshAIProxySubscription,
  DeleteAIProxySubscription,
} from '../../wailsjs/go/main/App'
import { aiproxy } from '../../wailsjs/go/models'
import { callApi } from './internal/call'

export type AIProxyState = aiproxy.State
export type AIProxySettings = aiproxy.Settings
export const getAIProxyState = () => callApi('[api.aiProxy.state]', GetAIProxyState)
export const saveAIProxySettings = (settings: AIProxySettings) => callApi('[api.aiProxy.save]', () => SaveAIProxySettings(new aiproxy.Settings(settings)))
export const startAIProxy = () => callApi('[api.aiProxy.start]', StartAIProxy)
export const stopAIProxy = () => callApi('[api.aiProxy.stop]', StopAIProxy)
export const syncAIProxyV2rayN = () => callApi('[api.aiProxy.sync]', SyncAIProxyV2rayN)
export const importAIProxySubscription = (name: string, url: string, content: string) => callApi('[api.aiProxy.import]', () => ImportAIProxySubscription(name, url, content))
export const refreshAIProxySubscription = (id: string) => callApi('[api.aiProxy.refresh]', () => RefreshAIProxySubscription(id))
export const deleteAIProxySubscription = (id: string) => callApi('[api.aiProxy.delete]', () => DeleteAIProxySubscription(id))
