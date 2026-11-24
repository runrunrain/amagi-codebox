package main

// bind_manifest_test.go — T-24 production Wails Bind surface test (design §4.1,
// §6.3, §10.3 C-01). Asserts that raw PTY services are NOT bound,
// and that the App exposes neither the cleanup bypass (StopAllSessions) nor the
// raw callback/history exports (R3-Minor-01).

import (
	"reflect"
	"strings"
	"testing"

	"amagi-codebox/internal/skins"
)

// forbiddenTypePrefixes are package-qualified type suffixes that must NEVER be
// in the production Bind list — the raw service objects themselves (design §6.3).
var forbiddenTypePrefixes = []string{
	"pty.Service",
	"secrets.SecretsService",
	"envcheck.Service",
	"logging.Service",
	"updater.Service",
	"ompconfig.Service",
	"opencodeconfig.Service",
	"sharedext.Service", // retired v1.3.145 depot; only registration counts are exposed
}

// appForbiddenMethods are App-level methods that must NOT be exported (raw
// cleanup bypass + raw callback/history surface — design §6.3, §10.3, §8.6.3).
// These are App-specific names that do not collide with legitimate services.
var appForbiddenMethods = []string{
	"StopAllSessions",  // §10.3 cleanup bypass
	"GetOutputHistory", // R3-Minor-01 raw history (no run token)
	"RegisterOutputCallback",
	"UnregisterOutputCallback",
	"RegisterExitCallback",
	"UnregisterExitCallback",
	"GetAllSharedSkillsAggregated", // don't scan private engine plugins for common skills
	"ReadTextFile",                 // import only reads files selected by the dialog
	"RegisterResizeCallback",
	"UnregisterResizeCallback",
}

// appGatedMutationMethods are the App exported methods that perform session/PTY
// mutation and MUST be the gated facade (routed through the ControlGate), not a
// raw passthrough (M-005: every mutation goes through the Gate). The test asserts
// these exist (the gated facade is reachable) AND that no raw bypass method leaks.
var appGatedMutationMethods = []string{
	"PtyWrite",
	"PtyWriteLarge",
	"PtyResize",
	"StopSession",
	"RemoveSession",
	"ClearStoppedSessions",
}

// appRawMutationNameFragments are name fragments that must NOT appear in any
// exported App method (they would indicate a raw service mutation leaked onto
// the bound App surface). Legitimate facades use the Pty*/Proxy* prefixes
// instead.
var appRawMutationNameFragments = []string{
	"WriteRaw",
	"ResizeRaw",
	"CloseSession",
	"CloseAll",
}

func TestBindManifest_NoRawServicesBound(t *testing.T) {
	app := newTestApp(t)
	bindList := buildWailsBindList(app)
	for _, obj := range bindList {
		typeName := reflect.TypeOf(obj).String()
		for _, prefix := range forbiddenTypePrefixes {
			if strings.HasSuffix(typeName, prefix) {
				t.Errorf("raw service %s is in the production Bind list (C-01 violation)", typeName)
			}
		}
	}
}

func TestBindManifest_AppForbiddenMethodsAbsent(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)
	for _, name := range appForbiddenMethods {
		if _, ok := appType.MethodByName(name); ok {
			t.Errorf("App exposes forbidden method %s (must be removed/unexported)", name)
		}
	}
}

func TestBindManifest_GatedFacadesReachable(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)
	for _, name := range []string{"PtyWrite", "PtyResize", "PtyWriteLarge", "GetOutputHistorySnapshot"} {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("App gated facade %s is missing", name)
		}
	}
}

// TestBindManifest_AppMutationSurfaceGated (M-005): the exported App methods
// that mutate session/PTY state are exactly the known gated facades, and no raw
// service mutation name fragment leaks onto the bound App surface. This turns
// the bind check from "raw service objects absent" into an exported-mutation
// manifest assertion.
// appClassifiedNonSessionMutations (R12 Minor): non-session/PTY mutations exported on
// the bound App surface that were introduced by M2 recovery work. Each entry
// is explicitly classified with its gating semantics so future manifest drift is
// reviewable: these are NOT raw session mutations (they do not touch PTY/session
// state), they require explicit user confirmation or backend authority fencing.
var appClassifiedNonSessionMutations = map[string]string{
	// Requires explicit PG-06-style user confirmation in the desktop UI; backend
	// refuses while the process is live and writes a typed audit on completion.
	"ConfirmExternalCleanupRecovery": "user-confirm + backend live-refuse + typed audit",
	// Claude 订阅账号轮换 facade（design §7.2）：入口在 App，raw 服务不入
	// Bind；所有 mutation 经 1a 领域服务的 MutationGate（SharedClaudeCredentials
	// mutation admission）+ operation 注册表；事件只发 operationId/stage/
	// state/generation。
	"BeginClaudeAccountEnrollment":  "credentials mutation admission + quiescence",
	"CaptureClaudeAccount":          "credentials mutation admission + quiescence + background operation",
	"CancelClaudeAccountEnrollment": "credentials mutation admission",
	"SwitchClaudeAccount":           "credentials mutation admission + quiescence + requestId 幂等 background operation",
	"SetClaudeAccountRotation":      "能力/池 revision/riskAck 校验 + ConfigService 写入",
	"ReconcileClaudeAccounts":       "inspect 零写；其余走 mutation 门 + background operation",
	"RemoveClaudeAccount":           "1a 移除限制（活动账号先切/停管）+ mutation admission",
	"RenameClaudeAccount":           "别名长度 1..64 + revision 校验",
	// 共用技能面板的非会话 mutation（扩展中心重构 spec §2.5/§2.8）：目录
	// 移动与删除均需 UI 确认弹窗（删除为 danger）；managed 名
	// （amagi-codebox）后端直接拒绝。
	"SetSharedSkillEnabled":            "目录移动（~/.agents/skills ⇄ skills-disabled）需 UI 确认弹窗；managed 名后端拒绝",
	"DeleteSharedSkill":                "技能目录永久删除需 UI danger 确认弹窗；managed 名后端拒绝",
	"ImportSharedSkillsZip":            "用户选择 ZIP；有界安全解包，默认停用，不覆盖同名",
	"ImportSharedSkillsDir":            "用户选择文件夹；有界复制，默认停用，不覆盖同名",
	"UninstallSharedMarketplacePlugin": "UI danger 确认；仅删除有匹配市场来源记录的技能",
	// 共用 MCP 面板的非会话 mutation（共用 MCP spec §3）：登记文件 CRUD 与
	// 四引擎分发写入均需 UI 表单/明确开关动作；外部手改的目标条目后端
	// 拒删待确认（ErrForeignModified → force 二次确认）。
	"CreateSharedMcpEntry":        "写 ~/.agents/.mcp.json（0600 原子写）需 UI 表单提交；同名已存在后端拒绝",
	"ImportPickedSharedMcpFile":   "确认并消耗当前文件预览凭据；不接受任意路径",
	"ImportSharedMcpEntries":      "UI 显式提交批量登记，不覆盖同名、不自动分发",
	"UpdateSharedMcpEntry":        "覆写登记条目需 UI 表单提交；name 不可改、条目不存在后端拒绝",
	"DeleteSharedMcpEntry":        "登记删除 + 已分发引擎还原需 UI danger 确认弹窗；外部手改的目标条目需 force 二次确认",
	"SetSharedMcpEngineSelection": "写各 Agent 真实 MCP 配置需 UI 明确开关/重新分发动作；目标手写同名条目后端拒绝（conflict）",
}

func TestBindManifest_ClassifiedNonSessionMutations(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)
	for name, classification := range appClassifiedNonSessionMutations {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("classified non-session mutation %s (%s) missing from App", name, classification)
		}
	}
}

func TestBindManifest_AppMutationSurfaceGated(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)

	// Every gated mutation method must be present (the facade is reachable).
	for _, name := range appGatedMutationMethods {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("gated mutation facade %s is missing from App", name)
		}
	}

	// No exported App method may carry a raw-mutation name fragment.
	for i := 0; i < appType.NumMethod(); i++ {
		m := appType.Method(i)
		for _, frag := range appRawMutationNameFragments {
			if strings.Contains(m.Name, frag) {
				t.Errorf("App exports raw-mutation method %s (must stay behind the gate)", m.Name)
			}
		}
	}
}

func TestBindManifest_AppIsBound(t *testing.T) {
	app := newTestApp(t)
	bindList := buildWailsBindList(app)
	var found bool
	for _, obj := range bindList {
		if _, ok := obj.(*App); ok {
			found = true
		}
	}
	if !found {
		t.Error("App object is not in the production Bind list")
	}
}

// TestBindManifest_SecretsDeadBindMethodsRemoved freezes the removal of the
// provider-specific key accessor shim methods that had no Go or frontend/src
// callers. SecretsService itself is no longer directly bound in bind_list.go
// (replaced by narrow App.HasProviderAPIKey / App.GetProviderAPIKey facades),
// and these dead shim methods must stay removed. GetAllProviders is
// intentionally retained: it has real Go callers in agentcli (handleKeyStatus
// in agentcli_h_config.go and knownStoredSecrets in agentcli_h_usage.go).
func TestBindManifest_SecretsDeadBindMethodsRemoved(t *testing.T) {
	app := newTestApp(t)
	if app.Secrets == nil {
		t.Fatal("app.Secrets is nil; secrets service not wired")
	}
	rt := reflect.TypeOf(app.Secrets)
	for _, name := range []string{"GetZhipuAPIKey", "SetZhipuAPIKey", "GetMinimaxAPIKey", "SetMinimaxAPIKey"} {
		if _, ok := rt.MethodByName(name); ok {
			t.Errorf("removed dead bind method %s reappeared on SecretsService", name)
		}
	}
	// Sanity: GetAllProviders must remain (agentcli callers depend on it).
	if _, ok := rt.MethodByName("GetAllProviders"); !ok {
		t.Errorf("GetAllProviders must remain on SecretsService (agentcli caller)")
	}
}

// TestBindManifest_ConfigServiceInjectionSurfaceReviewed (diting M3 复核):
// ConfigService 经 bind_list 整体绑定（设计允许），其注入/回写类方法随之暴露给
// 本地 webview。信任边界在本地 webview 内、沿袭 SetAPIKeyResolver 既有先例，
// 予以清单化接受——本测试冻结该注入面：Set*/Record* 方法集合必须恰好等于清单，
// 未来新增注入面必须在此显式登记其理由，防止静默扩张。
var configServiceAcceptedMutationSurface = map[string]string{
	"SetAPIKeyResolver":              "组装期注入 key 解析器（契约 §2 既有先例）",
	"SetAPIKeyReadyProbe":            "组装期注入密钥库就绪探针；未就绪时跳过导出写盘，防止空 key 覆盖（密钥面安全）",
	"SetModalityProber":              "组装期注入探测调度入口（契约 v1.2）",
	"SetPiLoginPresetChangeListener": "组装期注入登录预设保存/删除回调；把额度与图片输入覆写同步到 Pi 标准 models.json（锁外触发，不进 ConfigService 写路径）；无凭据面",
	"RecordModalityProbe":            "探测结论回写入口；webview 伪造仅污染本机能力缓存，无凭据外泄",
	"RecordProviderQuota":            "额度探测结论回写入口（额度查询方案 §4）；Entry 值域天然无凭据，webview 伪造仅污染本机额度缓存",
	"SetAgentTeams":                  "Agent Teams 设置的合法入口",
	"SetClaudeAccountRotation":       "Claude 订阅账号轮换配置唯一写入口（design §3.2：账号服务不自行写 models.json；严格 Validate 拒绝未知 strategy）",
	"ReplaceProviders":               "整设备配置导入的事务性提供商快照替换入口；经 scrubProviderAPIKeys 剥离明文密钥且无凭据落盘，仅影响本地 models.json",
	"ReplaceImportedPresetSnapshots": "整设备配置导入的预设快照整体替换入口（terminal_presets / opencode_presets）；仅更新本地 models.json 预设，无凭据与系统命令面",
}

func TestBindManifest_ConfigServiceInjectionSurfaceReviewed(t *testing.T) {
	app := newTestApp(t)
	if app.Config == nil {
		t.Fatal("app.Config is nil; config service not wired")
	}
	rt := reflect.TypeOf(app.Config)
	actual := map[string]bool{}
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		if strings.HasPrefix(name, "Set") || strings.HasPrefix(name, "Record") || strings.HasPrefix(name, "Replace") {
			actual[name] = true
		}
	}
	for name, reason := range configServiceAcceptedMutationSurface {
		if !actual[name] {
			t.Errorf("classified ConfigService mutation %s (%s) missing", name, reason)
		}
		delete(actual, name)
	}
	for name := range actual {
		t.Errorf("ConfigService exposes unreviewed Set*/Record*/Replace* method %s —— 注入/回写面扩张须在 bind_manifest_test.go 登记理由", name)
	}
}

func TestBindManifest_SkinsNoSetContext(t *testing.T) {
	rt := reflect.TypeOf((*skins.Service)(nil))
	if _, ok := rt.MethodByName("SetContext"); ok {
		t.Error("skins.Service must NOT expose SetContext on Wails bind surface")
	}
}

func TestBindManifest_AppHardenedSurface(t *testing.T) {
	app := newTestApp(t)
	appType := reflect.TypeOf(app)

	for _, name := range []string{"HasProviderAPIKey", "GetProviderAPIKey"} {
		if _, ok := appType.MethodByName(name); !ok {
			t.Errorf("App narrow facade %s is missing", name)
		}
	}
	for _, removed := range []string{
		"CleanHeadroom",
		"QuickLaunch",
		"GetEnvCheckOperationState",
		"GetConfigService",
		"GetPathsService",
		"GetSettingsService",
		"GetSessionWebUI",
		"ProbeSessionWebUI",
		"SaveAllConfig",
		"GetKeyDiagnostics",
		"ClearStoppedSessionsDetailed",
		"GetEnvCheckStatus",
		"GetLaunchCompensationDebts",
		"RetryLaunchCompensationDebt",
	} {
		if _, ok := appType.MethodByName(removed); ok {
			t.Errorf("App exposes hardened/internal/dead method %s (must not be bound)", removed)
		}
	}
}
