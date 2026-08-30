//go:build !bindings

package main

// bindingsGenerationMode 为 false 表示正常 GUI 运行模式（受单实例互斥保护）。
const bindingsGenerationMode = false
