//go:build bindings

package main

// bindingsGenerationMode 为 true 表示本进程是 wails 以 -tags bindings 构建的
// 绑定生成进程：只导出前端绑定即退出，不进入单实例互斥与 GUI 启动路径。
const bindingsGenerationMode = true
