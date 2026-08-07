// Package ompplugin manages Oh My Pi (omp) plugins (npm + marketplace).
//
// omp 插件管理走 CLI 驱动模式（对标 internal/piplugin 的 executor 模板）：
// 读操作（list）fork omp CLI 取 --json 输出并做字段级容错解析；写操作
// （install/uninstall/enable/disable/upgrade）走 omp CLI。管理范围是 omp 的
// 标准用户目录 ~/.omp/plugins——插件 CLI 自带目录语义，无需（也不应）注入
// PI_CODING_AGENT_DIR（详见 executor.go 头部注释）。
//
// 写操作通过 s.mu 串行化；读操作（List）不持锁，容忍安装中间态。
package ompplugin

import (
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"errors"
	"strings"
	"sync"
)

// ompExecutable 是 omp CLI 的可执行名（与 internal/envcheck/checker_omp.go 一致）。
const ompExecutable = "omp"

// Service manages omp plugins.
type Service struct {
	log      *logging.Service
	resolver platform.CLIResolver
	runner   platform.ProcessRunner
	// mu 串行化写操作（install/uninstall/enable/disable/upgrade），
	// 避免并发 CLI 写互相踩踏 omp 的注册表/实体目录。
	mu sync.Mutex
}

// NewService creates an omp plugin service.
func NewService(log *logging.Service) *Service {
	return NewServiceWithDeps(log,
		platform.NewCLIResolver(platform.CurrentCapabilities()),
		platform.NewProcessRunner())
}

// NewServiceWithDeps allows injecting a CLI resolver and process runner (tests).
func NewServiceWithDeps(log *logging.Service, resolver platform.CLIResolver, runner platform.ProcessRunner) *Service {
	return &Service{
		log:      log,
		resolver: resolver,
		runner:   runner,
	}
}

// cmdShellMetachars 是 Windows cmd.exe 的命令拼接元字符（与 piplugin 同源）。
// omp CLI 在 Windows 上经 npm 全局安装时入口通常是 .cmd，CodeBox 会用
// `cmd.exe /c omp.cmd ...` 执行（见 internal/platform/process_script_windows.go）。
// 这些字符若出现在安装目标中，会被 cmd.exe 解释为命令分隔/重定向，构成命令
// 注入面。合法目标（npm/git/local/marketplace ref）的语法均不含这些字符，
// 因此一律拒绝是安全的。
const cmdShellMetachars = `&|<>^%()`

// validatePluginSpec 校验安装目标/插件名，拒绝空/危险输入
// （对标 piplugin validateSource 的语义，错误信息说明 omp 安装目标非法）。
// 安全闸门：拒绝 cmd.exe 元字符，阻断 Windows .cmd wrapper 的命令注入路径。
func validatePluginSpec(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", errors.New("omp 安装目标不能为空")
	}
	if len(spec) > 2048 {
		return "", errors.New("omp 安装目标过长")
	}
	if strings.HasPrefix(spec, "-") || strings.ContainsAny(spec, "\x00\r\n") {
		return "", errors.New("omp 安装目标格式无效")
	}
	if strings.ContainsAny(spec, cmdShellMetachars) {
		return "", errors.New("omp 安装目标含命令行元字符，已拒绝（潜在的命令注入）")
	}
	return spec, nil
}

// ListPlugins 列出已安装的 omp 插件（npm + marketplace）。
// 读操作不持锁：容忍安装中间态，列表可能短暂缺条目。
func (s *Service) ListPlugins() ([]Plugin, error) {
	plugins, _, err := s.listPluginsUnlocked()
	return plugins, err
}

// listPluginsUnlocked 是 ListPlugins 的内部实现（供写操作在持锁下复用）。
func (s *Service) listPluginsUnlocked() ([]Plugin, []string, error) {
	result, err := s.executeOmpCommand(nil, ompListTimeout, "plugin", "list", "--json")
	if err != nil {
		return nil, nil, err
	}
	plugins, warnings, parseErr := parsePluginList(result.Output)
	if parseErr != nil {
		return nil, nil, parseErr
	}
	return plugins, warnings, nil
}

// RefreshPlugins 刷新并返回聚合数据（List + 解析降级 Warnings 信封）。
func (s *Service) RefreshPlugins() (*PluginsData, error) {
	plugins, warnings, err := s.listPluginsUnlocked()
	if err != nil {
		return nil, err
	}
	return &PluginsData{Installed: plugins, Warnings: warnings}, nil
}

// InstallPlugin 通过 omp CLI 安装插件（npm/git/local/marketplace ref 均支持）。
func (s *Service) InstallPlugin(spec string) (*CommandResult, error) {
	spec, err := validatePluginSpec(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executeOmpCommand(nil, ompInstallTimeout, "install", spec)
}

// UninstallPlugin 通过 omp CLI 卸载插件（npm 与 marketplace 由 CLI 自动路由）。
func (s *Service) UninstallPlugin(name string) (*CommandResult, error) {
	name, err := validatePluginSpec(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executeOmpCommand(nil, ompPluginWriteTimeout, "plugin", "uninstall", name)
}

// SetPluginEnabled 启用/禁用插件（omp plugin enable|disable <name>）。
func (s *Service) SetPluginEnabled(name string, enabled bool) (*CommandResult, error) {
	name, err := validatePluginSpec(name)
	if err != nil {
		return nil, err
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executeOmpCommand(nil, ompPluginWriteTimeout, "plugin", action, name)
}

// UpgradePlugin 升级单个插件：
//   - marketplace 插件 → omp plugin upgrade <id>（marketplace 升级语义）；
//   - npm 插件（或列表中未识别）→ omp install <name> --force（重装即升级，
//     对 npm/git/local 目标同样有效，作为未识别目标的兜底）。
func (s *Service) UpgradePlugin(name string) (*CommandResult, error) {
	name, err := validatePluginSpec(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plugins, _, listErr := s.listPluginsUnlocked()
	if listErr == nil {
		for i := range plugins {
			if plugins[i].ID == name || plugins[i].Name == name {
				if plugins[i].Kind == pluginKindMarketplace {
					return s.executeOmpCommand(nil, ompInstallTimeout, "plugin", "upgrade", plugins[i].ID)
				}
				return s.executeOmpCommand(nil, ompInstallTimeout, "install", name, "--force")
			}
		}
	}
	// 未在列表命中（含旧版 omp 降级为空列表）：按 npm 重装即升级兜底。
	return s.executeOmpCommand(nil, ompInstallTimeout, "install", name, "--force")
}
