import { apiClient, type SessionInfo } from '../api/client'
import { resolveAppType, type AppType } from '../types/terminal'

export interface SessionMetadata {
  sessionId: string
  appType: AppType
  provider: string
  model: string
  mode: string
  status: string
  workDir: string
}

export function mapSessionToMetadata(session: SessionInfo): SessionMetadata {
  return {
    sessionId: session.id,
    appType: resolveAppType(session.appType || session.mode),
    provider: session.provider,
    model: session.model || '',
    mode: session.mode,
    status: session.status,
    workDir: session.workDir,
  }
}

export async function fetchSessionMetadata(sessionId: string): Promise<SessionMetadata | null> {
  const sessions = await apiClient.getSessions()
  const session = sessions.find((item) => item.id === sessionId)
  return session ? mapSessionToMetadata(session) : null
}
