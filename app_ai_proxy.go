package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"amagi-codebox/internal/aiproxy"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

// Desktop-only facade. Credentials and raw nodes never enter Wails bindings.
func (a *App) GetAIProxyState() (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	return a.aiProxy.State(), nil
}
func (a *App) SaveAIProxySettings(settings aiproxy.Settings) (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	err := a.aiProxy.SaveSettings(settings)
	return a.aiProxy.State(), err
}
func (a *App) StartAIProxy() (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := a.aiProxy.Start(ctx)
	return a.aiProxy.State(), err
}
func (a *App) StopAIProxy() (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	err := a.aiProxy.Stop()
	return a.aiProxy.State(), err
}
func (a *App) SyncAIProxyV2rayN() (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.aiProxy.SyncV2rayN(ctx)
	return a.aiProxy.State(), err
}
func (a *App) ImportAIProxySubscription(name, rawURL, content string) (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := a.aiProxy.Import(ctx, name, rawURL, content)
	return a.aiProxy.State(), err
}
func (a *App) RefreshAIProxySubscription(id string) (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := a.aiProxy.Refresh(ctx, id)
	return a.aiProxy.State(), err
}
func (a *App) DeleteAIProxySubscription(id string) (aiproxy.State, error) {
	if a.aiProxy == nil {
		return aiproxy.State{}, errors.New("AI 代理服务未初始化")
	}
	err := a.aiProxy.Delete(id)
	return a.aiProxy.State(), err
}

func (a *App) aiProxyHTTPClient() *http.Client {
	if a.aiProxy == nil {
		return &http.Client{}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = a.aiProxy.ProxyForRequest
	return &http.Client{Transport: transport}
}

func (p *appLaunchPlanner) resolveLaunch(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	if p.networkEnv != nil {
		var err error
		req.Env, err = p.networkEnv(req.Env)
		if err != nil {
			return platform.ResolvedLaunchSpec{}, err
		}
	}
	// 环境转发规则与桌面 resolveEmbeddedLaunchSpec 对齐（remote create /
	// restart 与五 CLI 桌面启动消费同一选择）：envvars.json 自定义键进入
	// WSLENV 转发面，allowPathValue 键获得 /p 路径翻译与剥离豁免；会话落在
	// WSL 内时先剥离携带宿主盘符路径值的跨界变量（判定面/豁免面与桌面同一
	// 谓词 launcher.StripWSLHostPathEnvWithAllow，必须在解析器拼 WSLENV 清单
	// 之前完成），路径值不经 WSLENV 泄漏到 distro。
	// 探测披露：launchTargetsWSLFn 内含 distro 探测（wsl.exe -l -q，进程内
	// 缓存）——剥离必须在 Resolve 前发生，无法等解析后定谳；该探测与本
	// 计划内解析器的既有必要探测共享同一缓存，不额外增加次数。工作目录的
	// 目标判定不经此门（见 finalizeWorkdir，纯 spec.BootstrapMode 判据）。
	if p.envVars != nil {
		req.EnvForwardKeys = p.envVars.Keys()
		req.EnvPathFlagKeys = p.envVars.PathAllowKeys()
	}
	if launchTargetsWSLFn(req.LaunchMode, req.RequestedShellPath) {
		kept, stripped, allowedPath := launcher.StripWSLHostPathEnvWithAllow(req.Env, req.EnvForwardKeys, req.EnvPathFlagKeys)
		if p.envObserver != nil {
			if len(allowedPath) > 0 {
				p.envObserver("INFO", "已放行跨界路径值环境变量（per-key 放行 + /p 路径翻译）", strings.Join(allowedPath, ","))
			}
			if len(stripped) > 0 {
				p.envObserver("WARN", "已剥离携带 Windows 路径值的跨界环境变量", strings.Join(stripped, ","))
			}
		}
		if len(stripped) > 0 {
			req.Env = kept
		}
	}
	spec, err := p.resolver.Resolve(req)
	if err != nil {
		return spec, err
	}
	return aiproxy.PinProxyEnvironment(spec)
}
