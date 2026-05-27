export type ConnectionState = 'connecting' | 'connected' | 'disconnected' | 'error'

export type StructuredPartType = 'text' | 'markdown' | 'tool' | 'diff' | 'raw-terminal'
export type StructuredToolState = 'pending' | 'running' | 'completed' | 'error'
export type StructuredRawReason = 'ansi' | 'tui' | 'unsupported-pattern' | 'classifier-overflow'

export interface StructuredPartFramePayload {
  id: string
  type: StructuredPartType
  text?: string
  markdown?: string
  tool?: {
    name: string
    state: StructuredToolState
    title?: string
    inputPreview?: string
    outputPreview?: string
  }
  diff?: {
    text: string
    language?: 'diff'
  }
  raw?: {
    text: string
    reason: StructuredRawReason
  }
  source: {
    kind: 'pty'
    seqStart: number
    seqEnd: number
    appType?: string
  }
  createdAt: string
}

export type KnownTerminalFrameType = 'output' | 'exit' | 'input' | 'resize' | 'dimensions' | 'structured-part'

export interface TerminalFrame {
  type: KnownTerminalFrameType | string
  data?: string
  seq?: number
  structuredExpected?: boolean
  part?: StructuredPartFramePayload
  exitCode?: number
  cols?: number
  rows?: number
}

type MessageHandler = (frame: TerminalFrame) => void
type StateHandler = (state: ConnectionState) => void

export class TerminalWebSocket {
  private ws: WebSocket | null = null
  private url: string = ''
  private reconnectAttempts: number = 0
  private maxReconnectDelay: number = 30000
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private intentionallyClosed: boolean = false
  private _state: ConnectionState = 'disconnected'

  private onMessageHandlers: MessageHandler[] = []
  private onStateHandlers: StateHandler[] = []

  get state(): ConnectionState {
    return this._state
  }

  private setState(state: ConnectionState) {
    this._state = state
    this.onStateHandlers.forEach(h => h(state))
  }

  onMessage(handler: MessageHandler) {
    this.onMessageHandlers.push(handler)
    return () => {
      this.onMessageHandlers = this.onMessageHandlers.filter(h => h !== handler)
    }
  }

  onStateChange(handler: StateHandler) {
    this.onStateHandlers.push(handler)
    return () => {
      this.onStateHandlers = this.onStateHandlers.filter(h => h !== handler)
    }
  }

  connect(serverUrl: string, sessionId: string, token: string, mode: string = 'observer') {
    this.intentionallyClosed = false
    this.reconnectAttempts = 0

    const parsed = new URL(serverUrl)
    const protocol = parsed.protocol === 'https:' ? 'wss' : 'ws'
    const params = new URLSearchParams()
    if (token.trim()) {
      params.set('token', token)
    }
    params.set('mode', mode)
    this.url = `${protocol}://${parsed.host}/ws/terminal/${sessionId}?${params.toString()}`

    this.doConnect()
  }

  connectWithUrl(wsUrl: string) {
    this.intentionallyClosed = false
    this.reconnectAttempts = 0
    this.url = wsUrl
    this.doConnect()
  }

  private doConnect() {
    if (this.ws) {
      this.ws.onclose = null
      this.ws.close()
    }

    this.setState('connecting')

    try {
      this.ws = new WebSocket(this.url)
      this.ws.binaryType = 'arraybuffer'

      this.ws.onopen = () => {
        this.reconnectAttempts = 0
        this.setState('connected')
      }

      this.ws.onmessage = (event: MessageEvent) => {
        try {
          const frame: TerminalFrame = JSON.parse(
            typeof event.data === 'string'
              ? event.data
              : new TextDecoder().decode(event.data)
          )
          this.onMessageHandlers.forEach(h => h(frame))
        } catch (err) {
          console.error('Failed to parse WebSocket frame:', err)
        }
      }

      this.ws.onerror = () => {
        this.setState('error')
      }

      this.ws.onclose = () => {
        if (!this.intentionallyClosed) {
          this.setState('disconnected')
          this.scheduleReconnect()
        } else {
          this.setState('disconnected')
        }
      }
    } catch {
      this.setState('error')
      this.scheduleReconnect()
    }
  }

  private scheduleReconnect() {
    if (this.intentionallyClosed) return

    const delay = Math.min(
      1000 * Math.pow(2, this.reconnectAttempts),
      this.maxReconnectDelay
    )
    this.reconnectAttempts++

    this.reconnectTimer = setTimeout(() => {
      if (!this.intentionallyClosed) {
        this.doConnect()
      }
    }, delay)
  }

  sendInput(data: string) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      const encoded = btoa(unescape(encodeURIComponent(data)))
      this.ws.send(JSON.stringify({
        type: 'input',
        data: encoded,
      }))
    }
  }

  sendResize(cols: number, rows: number) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({
        type: 'resize',
        cols,
        rows,
      }))
    }
  }

  disconnect() {
    this.intentionallyClosed = true
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.ws) {
      this.ws.close()
      this.ws = null
    }
    this.setState('disconnected')
  }
}
