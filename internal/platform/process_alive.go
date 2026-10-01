package platform

// process_alive.go — pid 存活性判定的跨平台入口。
//
// ProcessAlive 判定给定 pid 的进程是否仍然存活。主要消费方：webui 会话
// 状态机在探测持续失败达阈值时校验 pi 进程存活性——重负载任务（长测试/
// 构建/大量输出）会阻塞扩展进程事件循环，/api/info 连续超时属"暂时不可达"
// 而非会话结束；pid 已死才是 ended（进程退出 → server 消亡）。
// 平台实现在 process_alive_unix.go / process_alive_windows.go。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return processAliveOS(pid)
}
