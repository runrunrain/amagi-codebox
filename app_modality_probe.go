package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"amagi-codebox/internal/config"
)

// 多模态能力实弹探测的 app 组装层（契约 docs/vision-export-contract.md v1.2/v1.3）。
//
// 两个入口共享同一执行路径 executeModalityProbe：
//   - 自动：ConfigService 在预设/服务商保存与配置加载后调度（非阻塞契约），
//     仅对「未手动标记、知识库（含学习层）未知、缓存未探」的模型触发；
//   - 手动：前端「探测能力」按钮调 ProbeModelModalityNow（Wails 绑定），用户
//     主动要求最新实证，绕过已知性判定直接探测。
//
// 有定论结论统一走 RecordModalityProbe 落盘：AppConfig 缓存（provider/model
// 维度）+ 设备端学习层（model 维度，跨 provider 泛化）+ 重同步 pi/omp 托管
// 配置（探测结论驱动托管条目的 input 声明；契约 v1.4 后不再改写视觉导出
// 文件，收录回归手动标记）。

// modalityProbeTimeout 单次探测总超时（/models 元数据 + 实弹图片请求共享）。
const modalityProbeTimeout = 30 * time.Second

// wireModalityProber 在 App 组装期注入探测调度入口（紧邻 SetAPIKeyResolver）。
func (a *App) wireModalityProber() {
	a.Config.SetModalityProber(func(providerName, model string) {
		go a.runModalityProbe(providerName, model)
	})
}

// ModalityProbeNowResult 手动探测的返回视图（Wails 绑定给前端按钮）。
type ModalityProbeNowResult struct {
	Conclusive bool   `json:"conclusive"` // 是否有定论（未决=环境故障，结论未落库）
	Vision     bool   `json:"vision"`     // 有定论时：是否支持图片理解
	Video      bool   `json:"video"`      // 有定论时：是否支持视频理解
	Source     string `json:"source,omitempty"`
	Message    string `json:"message"` // 面向用户的结果描述
}

// executeModalityProbe 执行单个 (provider, model) 的实弹探测：解析端点与
// key、限定时窗、调用 config.ProbeModelModalities。conclusive=false 表示未决。
func (a *App) executeModalityProbe(providerName, model string) (config.ModelModalities, string, bool, error) {
	provider, err := a.Config.GetProvider(providerName)
	if err != nil || provider == nil {
		return config.ModelModalities{}, "", false, errors.New("provider 不存在")
	}
	baseURL := provider.EffectiveBaseURL("openai")
	if baseURL == "" {
		return config.ModelModalities{}, "", false, errors.New("provider 无 OpenAI 兼容端点")
	}
	apiKey, _ := a.getProviderAPIKey(providerName, *provider)
	ctx, cancel := context.WithTimeout(context.Background(), modalityProbeTimeout)
	defer cancel()
	// client 不设备注超时：整体时限由 ctx 控制（30s），避免双超时语义打架。
	mods, source, conclusive := config.ProbeModelModalities(ctx, &http.Client{}, baseURL, apiKey, model)
	return mods, source, conclusive, nil
}

// commitModalityProbe 有定论结论的统一收尾：落缓存 + 学习层 + 重同步
// （旁路产物失败仅记日志）。
func (a *App) commitModalityProbe(providerName, model string, mods config.ModelModalities, source string) {
	if err := a.Config.RecordModalityProbe(providerName, model, mods, source, true); err != nil {
		if a.Log != nil {
			a.Log.Warn("modality-probe", "探测结论落盘失败", err.Error())
		}
		return
	}
	if a.Log != nil {
		a.Log.Info("modality-probe", "多模态能力探测完成: "+config.ModalityProbeKey(providerName, model)+" "+config.ModalityProbeEntry{Vision: mods.Vision, Video: mods.Video, Source: source}.String())
	}
	// 能力结论可能影响 pi/omp 托管条目的 input 声明（契约 v1.4：视觉导出文件
	// 收录回归手动标记，不受探测影响）：重跑统一同步（旁路产物，失败仅记
	// 日志，不告警用户）。
	if err := a.syncProvidersToHarnesses(); err != nil && a.Log != nil {
		a.Log.Warn("modality-probe", "探测后同步 harness 配置失败", err.Error())
	}
}

// runModalityProbe 自动路径：异步执行，in-flight 去重保证同一 provider/model
// 任意时刻至多一个自动探测在飞。
func (a *App) runModalityProbe(providerName, model string) {
	key := config.ModalityProbeKey(providerName, model)
	if _, loaded := a.modalityProbeInFlight.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	defer a.modalityProbeInFlight.Delete(key)

	mods, source, conclusive, err := a.executeModalityProbe(providerName, model)
	if err != nil || !conclusive {
		// 未决（网络/鉴权/限流/形态拒绝）：不落缓存，下次保存/启动自然重试。
		return
	}
	a.commitModalityProbe(providerName, model, mods, source)
}

// ProbeModelModalityNow 手动路径（Wails 绑定）：用户点击「探测能力」立即实弹。
// 绕过「已知性」判定（用户要的是当下实证），但与自动路径共享执行与收尾。
// 未决不落库，Message 说明环境原因；有定论返回能力并落库（含学习层回写）。
func (a *App) ProbeModelModalityNow(providerName, model string) ModalityProbeNowResult {
	if model == "" {
		if p, err := a.Config.GetProvider(providerName); err == nil && p != nil {
			model = p.DefaultModel
		}
	}
	if model == "" {
		return ModalityProbeNowResult{Message: "未指定模型，且 Provider 无默认模型"}
	}
	mods, source, conclusive, err := a.executeModalityProbe(providerName, model)
	if err != nil {
		return ModalityProbeNowResult{Message: err.Error()}
	}
	if !conclusive {
		return ModalityProbeNowResult{Message: "未决：网络/鉴权/限流或网关不接受探测形态，结论未落库（可稍后重试）"}
	}
	a.commitModalityProbe(providerName, model, mods, source)
	caps := "纯文本（不支持图片输入）"
	if mods.Vision && mods.Video {
		caps = "支持图片 + 视频理解"
	} else if mods.Vision {
		caps = "支持图片理解"
	} else if mods.Video {
		caps = "支持视频理解"
	}
	return ModalityProbeNowResult{
		Conclusive: true,
		Vision:     mods.Vision,
		Video:      mods.Video,
		Source:     source,
		Message:    "探测完成（已写入设备知识库）：" + caps,
	}
}
