package piconfig

import "fmt"

// CodeMode 是工具组合方式，无独立角色/任务授权。只验证调用偏好和可覆盖的运行默认值。
// 旧 mode/roles/rpc/聚合预算留存兼容，但不再作为准入条件。
func validateCodemode(obj map[string]any) error {
	raw, exists := obj["codemode"]
	if !exists {
		return nil
	}
	c, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("codemode 必须是对象")
	}
	if v, yes := c["preference"]; yes && v != "auto" && v != "prefer" {
		return fmt.Errorf("codemode.preference 只允许 auto/prefer")
	}
	if raw, yes := c["budget"]; yes {
		b, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("codemode.budget 必须是对象")
		}
		for k, max := range map[string]float64{"maxOutputTokensPerScript": 1000000, "maxTimeoutMsPerScript": 2147483647} {
			if v, yes := b[k]; yes {
				n, ok := v.(float64)
				if !ok || n < 1 || n > max || n != float64(int64(n)) {
					return fmt.Errorf("codemode.budget.%s 必须是 1–%.0f 的整数", k, max)
				}
			}
		}
	}
	return nil
}
