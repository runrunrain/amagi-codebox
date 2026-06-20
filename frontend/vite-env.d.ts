/// <reference types="vite/client" />

// Wails runtime global declaration
declare global {
  interface Window {
    go: {
      main: {
        App: typeof import('./wailsjs/go/main/App')
      }
    }
  }
}

export {}
