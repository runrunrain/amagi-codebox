// backfill.go 历史回填协商 + timeline 缓冲（蓝图 §6 流程 3、§8）：
//
// attach 时以 lastSeq+1 发起 backfill 协商；backfill.frames 结果按 seq 单调
// 消费入 timeline 缓冲；遇 backfill.gap / history.gap 不吞不改——fail-closed
// 交上层决策（提示 continue-from-latest，契约 ActionHint）。
package remoteclient
