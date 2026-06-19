/// <reference types="vite/client" />

declare module '*.vue' {
    import type {DefineComponent} from 'vue'
    const component: DefineComponent<{}, {}, any>
    export default component
}

// Wails runtime global declaration
declare global {
  interface Window {
    go: {
      main: {
        App: typeof import('../wailsjs/go/main/App')
      }
    }
  }
}

export {}
