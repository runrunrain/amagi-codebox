package sharedext

// mcp.go — 公共 MCP 管理：JSON 粘贴解析（三格式兼容预览）、批量导入、
// 列表、删除（先回收后清库）、启停与 targets diff 分发。

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ParseMCPJSON 纯解析预览（不落库）。兼容三种格式：
//  1. {"mcpServers": {name: def}}（Claude/.mcp.json/pi/omp 格式；宽容兼容
//     opencode 的 "mcp" 键）；
//  2. {"<name>": def, ...} 裸对象（def 必须是含 command 或 url 的对象）；
//  3. [{...}] / {name: def} 单条粘贴（数组元素递归按前两种格式解释）。
//
// 完全无法识别时报中文错误，说明支持的格式。
func ParseMCPJSON(text string) ([]SharedMCPServer, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("MCP JSON 内容为空；支持三种格式：1) {\"mcpServers\": {...}}；2) 裸对象 {\"名称\": {\"command\"|\"url\": ...}}；3) 单条粘贴（对象或数组）")
	}
	var root any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return nil, fmt.Errorf("不是合法 JSON: %w；支持三种格式：1) {\"mcpServers\": {...}}；2) 裸对象 {\"名称\": {\"command\"|\"url\": ...}}；3) 单条粘贴（对象或数组）", err)
	}

	servers := parseMCPValue(root)
	if len(servers) == 0 {
		return nil, errors.New("未能从内容中识别出任何 MCP server；支持三种格式：1) {\"mcpServers\": {\"名称\": {\"command\"|\"url\": ...}}}；2) 裸对象 {\"名称\": {\"command\"|\"url\": ...}}；3) 单条粘贴（对象或数组）")
	}
	return servers, nil
}

// parseMCPValue 递归解释已解析的 JSON 值（对象/数组），收集有效 server。
func parseMCPValue(v any) []SharedMCPServer {
	var out []SharedMCPServer
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			out = append(out, parseMCPValue(item)...)
		}
	case map[string]any:
		out = append(out, parseMCPObject(t)...)
	}
	return out
}

// parseMCPObject 解释一个 JSON 对象：优先识别 mcpServers/mcp 包裹键，
// 否则按裸对象格式（name→def，def 须为含 command 或 url 的对象）。
func parseMCPObject(obj map[string]any) []SharedMCPServer {
	var out []SharedMCPServer

	// 格式 1：mcpServers（或 opencode 的 mcp）包裹。
	for _, wrapper := range []string{"mcpServers", "mcp"} {
		if inner, ok := obj[wrapper].(map[string]any); ok && len(inner) > 0 {
			for name, def := range inner {
				// C3：含双引号/控制字符的名字不可入库（TOML/JSON 写入侧硬伤），
				// 预览阶段直接丢弃。
				if d, ok := def.(map[string]any); ok && validMCPName(name) {
					out = append(out, makeParsedServer(name, d))
				}
			}
			return out
		}
	}

	// 格式 2/3：裸对象 {name: def}；def 必须是含 command 或 url 的对象，
	// 否则视为不识别（避免把任意 JSON 误当 server 列表）。
	for name, def := range obj {
		d, ok := def.(map[string]any)
		if !ok || !validMCPName(name) {
			continue
		}
		if _, hasCommand := d["command"]; !hasCommand {
			if _, hasURL := d["url"]; !hasURL {
				continue
			}
		}
		out = append(out, makeParsedServer(name, d))
	}
	return out
}

// makeParsedServer 构造解析预览条目（深拷贝 def；名称非法的条目丢弃由
// 调用方聚合判断）。
func makeParsedServer(name string, def map[string]any) SharedMCPServer {
	return SharedMCPServer{
		Name:   name,
		Config: deepCopyMCPConfig(def),
		Origin: "manual",
	}
}

// validMCPName 判断 MCP server 名可入库（C3）：目录名安全（validName）且
// 不含双引号与控制字符——这两类字符在 TOML/JSON 写入侧都是硬伤，在导入
// 入口直接拒绝（预览阶段丢弃、ImportMCPServers 记 skipped）。含点/单引号
// 等其他边界字符的名字可在 JSON 引擎使用，分发到 codex 时由
// validateTOMLSectionName 给出明确错误（M1 保证部分失败可回收）。
func validMCPName(name string) bool {
	if !validName(name) {
		return false
	}
	if strings.ContainsRune(name, '"') {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ImportMCPServers 批量落库（Config 深拷贝防外泄引用）；同名已存在跳过并
// 聚合到 skipped。
func (s *Service) ImportMCPServers(servers []SharedMCPServer) (imported []SharedMCPServer, skipped []string, err error) {
	if len(servers) == 0 {
		return nil, nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return nil, nil, err
	}

	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if !validMCPName(name) {
			skipped = append(skipped, fmt.Sprintf("%s（名称非法）", server.Name))
			continue
		}
		if len(server.Config) == 0 {
			skipped = append(skipped, fmt.Sprintf("%s（缺少 server 定义）", name))
			continue
		}
		if _, exists := reg.MCP[name]; exists {
			skipped = append(skipped, fmt.Sprintf("%s（同名已存在，跳过）", name))
			continue
		}
		entry := SharedMCPServer{
			Name:      name,
			Config:    deepCopyMCPConfig(server.Config),
			Origin:    "manual",
			Enabled:   false,
			CreatedAt: nowRFC3339(),
		}
		reg.MCP[name] = entry
		imported = append(imported, entry)
	}

	if len(imported) > 0 {
		if err := s.saveRegistryLocked(reg); err != nil {
			return nil, nil, err
		}
		s.info("导入公共 MCP server", fmt.Sprintf("成功 %d 个，跳过 %d 个", len(imported), len(skipped)))
	}
	return imported, skipped, nil
}

// ListSharedMCPServers 返回 registry 全量 MCP server（按 name 字典序）。
func (s *Service) ListSharedMCPServers() ([]SharedMCPServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return nil, err
	}
	out := make([]SharedMCPServer, 0, len(reg.MCP))
	for _, server := range reg.MCP {
		out = append(out, server)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteSharedMCPServer 删除一条公共 MCP server：先按 Deployed 清单从各
// CLI 配置移除，再删 registry 条目。
func (s *Service) DeleteSharedMCPServer(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return err
	}
	server, ok := reg.MCP[name]
	if !ok {
		return fmt.Errorf("公共 MCP server %q 不存在", name)
	}

	if len(server.Deployed) > 0 {
		if err := s.undeployMCPLocked(&server, server.Deployed); err != nil {
			return fmt.Errorf("回收 MCP server %s 失败: %w", name, err)
		}
	}
	delete(reg.MCP, name)
	if err := s.saveRegistryLocked(reg); err != nil {
		return err
	}
	s.info("删除公共 MCP server", name)
	return nil
}

// SetSharedMCPServerEnabled 启用/停用一条公共 MCP server（语义同 skills）。
func (s *Service) SetSharedMCPServerEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return err
	}
	server, ok := reg.MCP[name]
	if !ok {
		return fmt.Errorf("公共 MCP server %q 不存在", name)
	}

	if enabled {
		if len(server.Targets) == 0 {
			// 未勾选目标时不分发，仅记录启停状态。
			server.Enabled = true
			reg.MCP[name] = server
			return s.saveRegistryLocked(reg)
		}
		deployErr := s.deployMCPLocked(&server, server.Targets)
		// M1：部分失败时已成功引擎的 Deployed 增量仍先落盘，再聚合返回
		// 错误（防状态漂移；语义同 skills 侧 SetSharedSkillEnabled）。
		server.Enabled = deployErr == nil || len(server.Deployed) > 0
		reg.MCP[name] = server
		if err := s.saveRegistryLocked(reg); err != nil {
			return err
		}
		return deployErr
	}
	undeployErr := s.undeployMCPLocked(&server, server.Deployed)
	// M1：部分回收失败也先落盘（已回收引擎移出 Deployed，失败者保留待重试）。
	server.Enabled = false
	reg.MCP[name] = server
	if err := s.saveRegistryLocked(reg); err != nil {
		return err
	}
	return undeployErr
}

// SetSharedMCPServerTargets 更新 MCP server 的目标引擎并 diff 分发
//（语义同 skills：新增分发、移除回收；Enabled=false 时只存不分发）。
func (s *Service) SetSharedMCPServerTargets(name string, targets []Engine) error {
	normalized, err := normalizeEngines(targets)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	reg, err := s.loadRegistryLocked()
	if err != nil {
		return err
	}
	server, ok := reg.MCP[name]
	if !ok {
		return fmt.Errorf("公共 MCP server %q 不存在", name)
	}

	old := server.Targets
	server.Targets = normalized

	if server.Enabled {
		var add, remove []Engine
		for _, e := range normalized {
			if !containsEngine(old, e) {
				add = append(add, e)
			}
		}
		for _, e := range old {
			if !containsEngine(normalized, e) {
				remove = append(remove, e)
			}
		}
		var opErrs []error
		if len(remove) > 0 {
			if err := s.undeployMCPLocked(&server, remove); err != nil {
				opErrs = append(opErrs, err)
			}
		}
		if len(add) > 0 {
			if err := s.deployMCPLocked(&server, add); err != nil {
				opErrs = append(opErrs, err)
			}
		}
		reg.MCP[name] = server
		if err := s.saveRegistryLocked(reg); err != nil {
			return err
		}
		// M1：部分失败先落盘实际 Deployed 状态，再返回聚合错误。
		return joinErrors(opErrs)
	}
	reg.MCP[name] = server
	return s.saveRegistryLocked(reg)
}
