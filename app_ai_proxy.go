package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"amagi-codebox/internal/aiproxy"
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
	spec, err := p.resolver.Resolve(req)
	if err != nil {
		return spec, err
	}
	return aiproxy.PinProxyEnvironment(spec)
}
