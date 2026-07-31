package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pi_sync.go — 全局 pi agent 状态继承（~/.pi/agent → CodeBox 托管 pi-runtime）。
//
// 背景：CodeBox 启动 Pi 会话时注入 PI_CODING_AGENT_DIR=<configDir>/pi-runtime
// 做写入隔离（不污染用户全局 ~/.pi/agent，见 pi_config.go）。但隔离目录是
// 全量替换而非叠加：pi 只从 PI_CODING_AGENT_DIR 读取 auth.json（账号认证）、
// settings.json（packages 插件注册）与 git/npm 包实体，导致用户在全局
// ~/.pi/agent 已有的登录态（如 kimi-coding/openai-codex）与已装插件（如
// amagi-pi）在 CodeBox 启动的终端与插件面板里完全不可见。
//
// 本文件提供"只读继承"：把全局状态合并/链接进 pi-runtime，写入仍隔离——
//   1. auth.json    ：全局键补缺（runtime 已有键优先，保证 pi 刷新 OAuth 后
//      写回 runtime 的新 token 不被全局旧值覆盖）。
//   2. settings.json：顶层键全局补缺；packages[] 按 source 去重合并
//      （runtime 条目优先），插件注册随之继承。
//   3. git/ npm 实体目录：runtime 缺失时建符号链接指向全局（只读共享包缓存，
//      避免拷贝数百 MB；pi 解析 <agentDir>/git/<host>/<user>/<project> 时
//      透明命中全局实体）。
//   4. models.json providers：写运行时 models.json 前由 MergeGlobalPiProviders
//      把全局 providers 并入（amagi 托管条目优先），pi 侧可见全部服务商。
//
// 全部操作幂等、best-effort：全局目录不存在/不可读时静默跳过，不阻断启动。

// GlobalPiAgentDir 复刻 pi getAgentDir 的解析：优先 $PI_CODING_AGENT_DIR，
// 否则 ~/.pi/agent。用于定位要继承的全局状态来源。
func GlobalPiAgentDir() string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// SyncPiGlobalState 把全局 pi agent 状态继承进 agentDir（CodeBox 托管 runtime）。
// logf 可为 nil；非 nil 时接收跳过/失败的告警（继承是增强而非硬依赖，全部非致命）。
func SyncPiGlobalState(agentDir string, logf func(format string, args ...any)) error {
	agentDir = strings.TrimSpace(agentDir)
	if agentDir == "" {
		return fmt.Errorf("agentDir is required")
	}
	globalDir := GlobalPiAgentDir()
	return syncPiGlobalStateFrom(agentDir, globalDir, logf)
}

// syncPiGlobalStateFrom 是 SyncPiGlobalState 的可测试内核（显式注入全局目录）。
func syncPiGlobalStateFrom(agentDir, globalDir string, logf func(format string, args ...any)) error {
	warn := func(format string, args ...any) {
		if logf != nil {
			logf(format, args...)
		}
	}
	if globalDir == "" || samePath(agentDir, globalDir) {
		return nil
	}
	if info, err := os.Stat(globalDir); err != nil || !info.IsDir() {
		return nil
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("mkdir pi agent dir: %w", err)
	}
	// 与 WritePiAgentConfig 对齐：MkdirAll 不收紧已存在目录的权限（如旧版本/手动创建的 0755），
	// 显式 Chmod 覆盖升级场景（diting-quick 快审 FINDING-1）。
	if err := os.Chmod(agentDir, 0o700); err != nil {
		return fmt.Errorf("chmod pi agent dir: %w", err)
	}

	if changed, err := mergePiAuthFile(agentDir, globalDir); err != nil {
		warn("继承 pi auth.json 失败: %v", err)
	} else if changed {
		warn("已从全局 pi 继承账号认证（auth.json）")
	}

	if changed, err := mergePiSettingsFile(agentDir, globalDir); err != nil {
		warn("继承 pi settings.json 失败: %v", err)
	} else if changed {
		warn("已从全局 pi 继承设置/插件注册（settings.json）")
	}

	// git/npm 包实体目录：缺失时符号链接到全局（只读共享；不拷贝大目录）。
	for _, name := range []string{"git", "npm"} {
		if err := linkPiEntityDir(agentDir, globalDir, name); err != nil {
			warn("链接 pi %s 实体目录失败: %v", name, err)
		}
	}
	return nil
}

// samePath 判断两个路径是否指向同一目录（Clean 后比较；另处理符号链接）。
func samePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	if ra, err := filepath.EvalSymlinks(ca); err == nil {
		if rb, err := filepath.EvalSymlinks(cb); err == nil && ra == rb {
			return true
		}
	}
	return false
}

// mergePiAuthFile 合并 auth.json：全局键补缺，agentDir 已有键优先。
// 返回是否发生写入。文件不存在按空对象处理；任何一端非法 JSON 时跳过该端。
func mergePiAuthFile(agentDir, globalDir string) (bool, error) {
	global := readJSONObject(filepath.Join(globalDir, "auth.json"))
	if len(global) == 0 {
		return false, nil
	}
	localPath := filepath.Join(agentDir, "auth.json")
	local := readJSONObject(localPath)

	merged := make(map[string]any, len(global)+len(local))
	for k, v := range global {
		merged[k] = v
	}
	changed := false
	for k, v := range local {
		merged[k] = v // runtime 优先（pi 刷新 token 写回 runtime，不能被全局旧值覆盖）
	}
	// 与 local 比较：出现新键才需要写。
	for k := range global {
		if _, ok := local[k]; !ok {
			changed = true
			break
		}
	}
	if !changed {
		return false, nil
	}
	if err := writeJSONObjectAtomic(localPath, merged, 0o600); err != nil {
		return false, fmt.Errorf("write merged auth.json: %w", err)
	}
	return true, nil
}

// mergePiSettingsFile 合并 settings.json：顶层键全局补缺（runtime 优先）；
// packages[] 按 source 去重合并（元素可为字符串源或 {source,...} 过滤对象）。
func mergePiSettingsFile(agentDir, globalDir string) (bool, error) {
	global := readJSONObject(filepath.Join(globalDir, "settings.json"))
	if len(global) == 0 {
		return false, nil
	}
	localPath := filepath.Join(agentDir, "settings.json")
	local := readJSONObject(localPath)

	changed := false
	merged := make(map[string]any, len(global)+len(local))
	for k, v := range global {
		merged[k] = v
	}
	for k, v := range local {
		merged[k] = v
	}
	// 顶层键补缺检测。
	for k := range global {
		if k == "packages" {
			continue
		}
		if _, ok := local[k]; !ok {
			changed = true
			break
		}
	}

	// packages 并集：runtime 条目优先，按 source identity 去重。
	if pkgUnion, added := unionPiPackages(local["packages"], global["packages"]); added {
		merged["packages"] = pkgUnion
		changed = true
	}

	if !changed {
		return false, nil
	}
	if err := writeJSONObjectAtomic(localPath, merged, 0o600); err != nil {
		return false, fmt.Errorf("write merged settings.json: %w", err)
	}
	return true, nil
}

// unionPiPackages 合并两个 packages 数组（元素：字符串源 或 {source,...} 对象）。
// 返回 (并集, 是否新增了全局条目)。local 在前且优先；按 source identity 去重。
func unionPiPackages(local, global any) ([]any, bool) {
	toSlice := func(v any) []any {
		arr, ok := v.([]any)
		if !ok {
			return nil
		}
		return arr
	}
	sourceOf := func(item any) string {
		switch e := item.(type) {
		case string:
			return strings.TrimSpace(e)
		case map[string]any:
			if s, ok := e["source"].(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}

	localArr := toSlice(local)
	globalArr := toSlice(global)
	if len(globalArr) == 0 {
		return localArr, false
	}
	seen := make(map[string]struct{}, len(localArr)+len(globalArr))
	out := make([]any, 0, len(localArr)+len(globalArr))
	for _, item := range localArr {
		if src := sourceOf(item); src != "" {
			seen[src] = struct{}{}
		}
		out = append(out, item)
	}
	added := false
	for _, item := range globalArr {
		src := sourceOf(item)
		if src == "" {
			continue
		}
		if _, dup := seen[src]; dup {
			continue
		}
		seen[src] = struct{}{}
		out = append(out, item)
		added = true
	}
	return out, added
}

// linkPiEntityDir 在 agentDir 缺失实体目录（git/npm）时建符号链接指向全局目录。
// 已存在真实目录时不触碰；符号链接完好时幂等跳过；断链（Lstat 在但目标不可达，
// 如全局目录被移动）时移除重建——自愈（diting-quick 快审 FINDING-2）。
func linkPiEntityDir(agentDir, globalDir, name string) error {
	localPath := filepath.Join(agentDir, name)
	if info, err := os.Lstat(localPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return nil // 真实目录，幂等跳过
		}
		if _, statErr := os.Stat(localPath); statErr == nil {
			return nil // 链接完好，幂等跳过
		}
		_ = os.Remove(localPath) // 断链：移除后重建
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	globalPath := filepath.Join(globalDir, name)
	info, err := os.Stat(globalPath)
	if err != nil || !info.IsDir() {
		return nil // 全局没有该实体目录，无需链接
	}
	if err := os.Symlink(globalPath, localPath); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", localPath, globalPath, err)
	}
	return nil
}

// MergeGlobalPiProviders 把全局 models.json 的 providers 并入待写入的 cfg
// （cfg 中已有的 amagi 托管条目优先）。globalDir 为空/不存在/等于 agentDir
// 时原样返回。用于 WritePiAgentConfig 之前调用，使 CodeBox 启动的 pi 同时
// 可见用户在全局 models.json 注册的自定义服务商。
func MergeGlobalPiProviders(cfg map[string]any, agentDir string) map[string]any {
	globalDir := GlobalPiAgentDir()
	if globalDir == "" || samePath(agentDir, globalDir) {
		return cfg
	}
	return mergePiProvidersFrom(cfg, globalDir)
}

// mergePiProvidersFrom 是 MergeGlobalPiProviders 的可测试内核。
func mergePiProvidersFrom(cfg map[string]any, globalDir string) map[string]any {
	global := readJSONObject(filepath.Join(globalDir, "models.json"))
	rawProviders, ok := global["providers"].(map[string]any)
	if !ok || len(rawProviders) == 0 {
		return cfg
	}
	local := piProviderEntries(cfg["providers"])
	merged := make(map[string]any, len(rawProviders)+len(local))
	for k, v := range rawProviders {
		merged[k] = v
	}
	for k, v := range local {
		merged[k] = v // amagi 托管条目优先
	}
	out := make(map[string]any, len(cfg)+1)
	for k, v := range cfg {
		out[k] = v
	}
	out["providers"] = merged
	return out
}

// piProviderEntries 把 cfg["providers"] 归一化为 map[string]any。
// BuildPiModelsConfig 产出的是 map[string]map[string]any（强类型字面量），
// 而 readJSONObject 反序列化得到 map[string]any——两种形态都必须兼容，
// 否则类型断言失败会静默丢弃 amagi 托管 provider（pi 报 Unknown provider）。
func piProviderEntries(v any) map[string]any {
	out := make(map[string]any)
	switch p := v.(type) {
	case map[string]any:
		for k, val := range p {
			out[k] = val
		}
	case map[string]map[string]any:
		for k, val := range p {
			out[k] = val
		}
	}
	return out
}

// readJSONObject 读取一个 JSON 对象文件；不存在/非法/非对象时返回空 map。
func readJSONObject(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return map[string]any{}
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return map[string]any{}
	}
	return obj
}

// writeJSONObjectAtomic 全代码库统一的原子写入范式：MarshalIndent -> tmp -> Rename，
// 并在各阶段收紧权限（对齐 WritePiAgentConfig 的 P1-7 安全姿态）。
func writeJSONObjectAtomic(path string, obj map[string]any, perm os.FileMode) error {
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return fmt.Errorf("write %s tmp: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod %s tmp: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
	}
	return nil
}
