export interface TerminalOutputRoutingFrame {
  structuredExpected?: boolean
  seq?: number
}

export interface TerminalOutputRoutingDeps {
  writeRawOutput: (text: string) => void
  scheduleStructuredFallback: (seq: number, text: string) => void
  enqueueTranscriptChunk: (text: string) => void
  scheduleRawTextViewSync: (text: string) => void
}

export function routeDecodedTerminalOutput(
  frame: TerminalOutputRoutingFrame,
  decodedOutput: string,
  deps: TerminalOutputRoutingDeps,
) {
  deps.writeRawOutput(decodedOutput)

  if (frame.structuredExpected === true && typeof frame.seq === 'number') {
    deps.scheduleStructuredFallback(frame.seq, decodedOutput)
    return
  }

  deps.enqueueTranscriptChunk(decodedOutput)
  deps.scheduleRawTextViewSync(decodedOutput)
}
