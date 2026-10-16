package wslsupply

// transform_test.go — D3 形态转换器矩阵（design §五确定规则全表，P3-S2 验收 3）。
// 转换器是纯函数：全部用例只喂已验证事实（TransformFacts），不触达真实 WSL。

import (
	"strings"
	"testing"
)

func testFacts() TransformFacts {
	return TransformFacts{
		ResolvedCommands: map[string]string{"npx": "/usr/bin/npx", "npm": "/usr/bin/npm"},
		PathMappings: map[string]string{
			normalizeForTest(`C:\tools\server.js`): "/opt/tools/server.js",
		},
		PrefixMappings: []PathPrefixRule{
			{WindowsPrefix: `C:\Users\u\.pi\agent`, LinuxPrefix: "/home/u/.pi/agent"},
		},
		HostHomeWindows: `C:\Users\u`,
	}
}

func normalizeForTest(s string) string { return NormalizeWindowsPath(s) }

func mcpEntry(kvs ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(kvs); i += 2 {
		key, _ := kvs[i].(string)
		out[key] = kvs[i+1]
	}
	return out
}

func TestTransformBareShimMappedOnlyWhenVerified(t *testing.T) {
	facts := testFacts()
	// 已验证：npx.cmd → npx。
	got := TransformMCPServer(mcpEntry("type", "stdio", "command", "npx.cmd"), facts)
	if got.Outcome != TransformKept || got.Entry["command"] != "npx" {
		t.Fatalf("verified bare shim must map to native npx: %+v", got)
	}
	// 未验证（探测失败面：空事实）→ 拒绝，不生成假可用条目。
	got = TransformMCPServer(mcpEntry("command", "npx.cmd"), TransformFacts{})
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "原生 npx") {
		t.Fatalf("unverified bare shim must be rejected: %+v", got)
	}
}

func TestTransformShimPathOnlyKnownTools(t *testing.T) {
	facts := testFacts()
	// C:\...\npx.cmd 识别为已支持工具 shim → 已验证原生命令替换。
	got := TransformMCPServer(mcpEntry("command", `C:\Program Files\nodejs\npx.CMD`), facts)
	if got.Outcome != TransformKept || got.Entry["command"] != "npx" {
		t.Fatalf("known tool shim path must map to native npx: %+v", got)
	}
	// 同路径但目标未验证 → 拒绝。
	got = TransformMCPServer(mcpEntry("command", `C:\Program Files\nodejs\npx.cmd`), TransformFacts{})
	if got.Outcome != TransformRejected {
		t.Fatalf("unverified shim path must be rejected: %+v", got)
	}
	// 同名 .cmd 但不在标准安装位置（文件名相同不证明行为等价）→ 拒绝该导入单元。
	got = TransformMCPServer(mcpEntry("command", `C:\tools\mytool\npx.cmd`), facts)
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "标准位置") {
		t.Fatalf("unknown-location shim must be rejected: %+v", got)
	}
}

func TestTransformWindowsShellFormsRejected(t *testing.T) {
	facts := testFacts()
	for _, cmd := range []string{
		"cmd.exe", "cmd", "powershell", "powershell.exe", "pwsh", "pwsh.exe",
		`C:\scripts\run.ps1`, `C:\tools\server.exe`, `C:\tools\run.bat`,
	} {
		got := TransformMCPServer(mcpEntry("command", cmd), facts)
		if got.Outcome != TransformRejected {
			t.Fatalf("windows shell form %q must be rejected: %+v", cmd, got)
		}
		if !strings.Contains(got.Reason, "需手动适配") {
			t.Fatalf("rejection must carry manual-adaptation reason, got %q", got.Reason)
		}
	}
	// 非 Windows 形态命令：原样保留（存在性验证属运行态，不在静态转换面冒充）。
	got := TransformMCPServer(mcpEntry("command", "uvx", "args", []any{"mcp-server-fetch"}), facts)
	if got.Outcome != TransformKept || got.Entry["command"] != "uvx" {
		t.Fatalf("plain posix command must be kept: %+v", got)
	}
}

func TestTransformArgsPathMapping(t *testing.T) {
	facts := testFacts()
	// 已部署资产前缀映射。
	got := TransformMCPServer(mcpEntry("command", "node", "args", []any{
		`C:\Users\u\.pi\agent\scripts\bridge.js`, "--flag",
	}), facts)
	if got.Outcome != TransformKept {
		t.Fatalf("deployed-asset arg must map: %+v (%s)", got, got.Reason)
	}
	args := got.Entry["args"].([]any)
	if args[0] != "/home/u/.pi/agent/scripts/bridge.js" {
		t.Fatalf("arg not mapped to linux path: %v", args[0])
	}
	// wslpath 实测精确映射。
	got = TransformMCPServer(mcpEntry("command", "node", "args", []any{`C:\tools\server.js`}), facts)
	if got.Outcome != TransformKept || got.Entry["args"].([]any)[0] != "/opt/tools/server.js" {
		t.Fatalf("verified path mapping must apply: %+v", got)
	}
	// 未经验证的盘符路径 → 拒绝（不盲信 /mnt 挂载）。
	got = TransformMCPServer(mcpEntry("command", "node", "args", []any{`D:\data\server.js`}), facts)
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "args[0]") {
		t.Fatalf("unverified drive arg must be rejected with field path: %+v", got)
	}
	// 内嵌路径形态（--config=C:\x）→ 保守拒绝。
	got = TransformMCPServer(mcpEntry("command", "node", "args", []any{"--config=" + `C:\Users\u\cfg.ini`}), facts)
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "内嵌") {
		t.Fatalf("embedded drive arg must be rejected: %+v", got)
	}
}

func TestTransformEnvPathRules(t *testing.T) {
	facts := testFacts()
	// 声明为路径的键（NODE_PATH：`;` 列表 → `:` 列表，逐段映射）。
	got := TransformMCPServer(mcpEntry("command", "node", "env", map[string]any{
		"NODE_PATH": `C:\Users\u\.pi\agent\lib;C:\tools\server.js`,
	}), facts)
	if got.Outcome != TransformKept {
		t.Fatalf("declared path env must convert: %+v (%s)", got, got.Reason)
	}
	env := got.Entry["env"].(map[string]any)
	if env["NODE_PATH"] != "/home/u/.pi/agent/lib:/opt/tools/server.js" {
		t.Fatalf("NODE_PATH not converted to colon list: %v", env["NODE_PATH"])
	}
	// %USERPROFILE% 在已确认基目录下展开后映射。
	got = TransformMCPServer(mcpEntry("command", "node", "env", map[string]any{
		"NODE_PATH": `%USERPROFILE%\.pi\agent\lib`,
	}), facts)
	if got.Outcome != TransformKept || got.Entry["env"].(map[string]any)["NODE_PATH"] != "/home/u/.pi/agent/lib" {
		t.Fatalf("%%USERPROFILE%% must expand under confirmed base: %+v", got)
	}
	// 未知变量 → 无法确定即拒绝（要求用户提供 Linux 值）。
	got = TransformMCPServer(mcpEntry("command", "node", "env", map[string]any{
		"NODE_PATH": `%MYTOOLS%\lib`,
	}), facts)
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "NODE_PATH") {
		t.Fatalf("unknown var must be rejected naming the key: %+v", got)
	}
	// 声明键但不可映射 → 拒绝。
	got = TransformMCPServer(mcpEntry("command", "node", "env", map[string]any{
		"NODE_PATH": `D:\nowhere\lib`,
	}), facts)
	if got.Outcome != TransformRejected {
		t.Fatalf("unmappable declared path must be rejected: %+v", got)
	}
	// 未声明键携带 Windows 路径形态 → 阻止该条目并提示键名（不静默删 env）。
	got = TransformMCPServer(mcpEntry("command", "node", "env", map[string]any{
		"SOME_TOKEN":  "sk-plain-value",
		"MY_TOOL_DIR": `C:\tools`,
	}), facts)
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "MY_TOOL_DIR") {
		t.Fatalf("undeclared windows-path env must be blocked naming the key: %+v", got)
	}
	// 普通 env 值（URL/token/${VAR} 占位符）不受影响。
	got = TransformMCPServer(mcpEntry("command", "npx", "env", map[string]any{
		"ZAI_API_KEY":    "${PI_PROVIDER_KEY:amagi-glm}",
		"TAVILY_API_KEY": "tvly-dev-xyz",
	}), facts)
	if got.Outcome != TransformKept {
		t.Fatalf("plain env values must be kept: %+v (%s)", got, got.Reason)
	}
}

func TestTransformRemoteMCCKeptVerbatim(t *testing.T) {
	facts := testFacts()
	entry := mcpEntry("type", "http", "url", "http://localhost:3333/mcp", "env", map[string]any{
		"TOKEN": `C:\not-a-path-check`,
	})
	got := TransformMCPServer(entry, facts)
	if got.Outcome != TransformKept || got.Entry["url"] != "http://localhost:3333/mcp" {
		t.Fatalf("remote MCP must keep URL verbatim: %+v", got)
	}
	// localhost 不自动改宿主 IP；env 原样保留。
	if got.Entry["env"].(map[string]any)["TOKEN"] != `C:\not-a-path-check` {
		t.Fatalf("remote env must be untouched: %+v", got)
	}
}

func TestMapWindowsPathRules(t *testing.T) {
	facts := testFacts()
	cases := []struct {
		in   string
		want string
	}{
		{`C:\Users\u\.pi\agent`, "/home/u/.pi/agent"},
		{`c:/users/U/.pi/agent/settings.json`, "/home/u/.pi/agent/settings.json"},
		{`C:\Users\U\.PI\AGENT\mcp.json`, "/home/u/.pi/agent/mcp.json"}, // 大小写不敏感
		{`C:\Tools\Server.js`, "/opt/tools/server.js"},                  // 规范化精确映射
		{`D:\missing\x.js`, ""},
		{`relative\path`, ""},
	}
	for _, tc := range cases {
		if got := MapWindowsPath(tc.in, facts); got != tc.want {
			t.Fatalf("MapWindowsPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTransformSettingValuePathOrDrop(t *testing.T) {
	facts := testFacts()
	// 白名单字段内独立路径 → 映射改写。
	got, ok := TransformSettingValue(`C:\Users\u\.pi\agent\data`, facts)
	if !ok || got != "/home/u/.pi/agent/data" {
		t.Fatalf("setting path value must map: %v %v", got, ok)
	}
	// 不可映射 → 剔除（ok=false），不保留 Windows 路径也不猜测改写。
	if _, ok := TransformSettingValue(`D:\gone`, facts); ok {
		t.Fatalf("unmappable setting path must be dropped")
	}
	// 内嵌路径形态 → 剔除（不做正文替换）。
	if _, ok := TransformSettingValue("prefix "+`C:\Users\u\x`+" suffix", facts); ok {
		t.Fatalf("embedded path in setting value must be dropped")
	}
	// 纯标量/嵌套对象数值不受影响。
	got, ok = TransformSettingValue(map[string]any{"provider": map[string]any{"timeoutMs": 300000}}, facts)
	if !ok {
		t.Fatalf("numeric setting subtree must pass through")
	}
	_ = got
}

func TestCollectTransformNeeds(t *testing.T) {
	entries := []map[string]any{
		mcpEntry("command", "npx.cmd", "args", []any{`C:\Users\u\.pi\agent\a.js`}),
		mcpEntry("command", `C:\nodejs\npm.cmd`),
		mcpEntry("command", "node", "env", map[string]any{"NODE_PATH": `C:\tools\server.js`}),
		mcpEntry("url", "https://remote.example/mcp"),
	}
	commands, paths := CollectTransformNeeds(entries)
	if len(commands) != 2 || commands[0] != "npm" || commands[1] != "npx" {
		t.Fatalf("commands to verify = %v", commands)
	}
	if len(paths) != 2 {
		t.Fatalf("paths to verify = %v", paths)
	}
}

func TestTransformURLsAreNotDrivePaths(t *testing.T) {
	facts := testFacts()
	// F-2（S2 diting）：scheme:// 不是盘符——未声明 env 键的 API endpoint URL
	// 与 args 内嵌 URL 放行（设计 §五「不把 URL、token 当路径」）。
	got := TransformMCPServer(mcpEntry("command", "npx", "env", map[string]any{
		"API_BASE":  "https://api.x.com/v1",
		"PG_DSN":    "postgres://user:pw@db.example:5432/x",
		"PLAIN/TOK": "sk-abc123",
	}), facts)
	if got.Outcome != TransformKept {
		t.Fatalf("URL env values must not be rejected as drive paths: %+v (%s)", got, got.Reason)
	}
	got = TransformMCPServer(mcpEntry("command", "uvx", "args", []any{"--endpoint=http://x.example"}), facts)
	if got.Outcome != TransformKept {
		t.Fatalf("args with embedded URL must pass: %+v (%s)", got, got.Reason)
	}
	// 真盘符仍拒（词首锚定不放松）。
	got = TransformMCPServer(mcpEntry("command", "npx", "env", map[string]any{
		"MY_TOOL_DIR": `prefix C:\tools suffix`,
	}), facts)
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "MY_TOOL_DIR") {
		t.Fatalf("true embedded drive path must still be rejected: %+v", got)
	}
}

func TestTransformUndeclaredVarPathEnvRejected(t *testing.T) {
	facts := testFacts()
	// F-5（S2 diting）：%VAR%+分隔符形态在未声明键中是运行时不可用的 Windows
	// 变量路径——点名键名拒绝该条目（无法确定则要求 Linux 值）。
	got := TransformMCPServer(mcpEntry("command", "npx", "env", map[string]any{
		"TOOL_HOME": `%USERPROFILE%\.tool`,
	}), facts)
	if got.Outcome != TransformRejected || !strings.Contains(got.Reason, "TOOL_HOME") {
		t.Fatalf("%%VAR%% path form in undeclared env must be rejected naming the key: %+v", got)
	}
	// ${VAR} 占位符（运行时解析）与裸 %VAR%（无路径形态）不受影响。
	got = TransformMCPServer(mcpEntry("command", "npx", "env", map[string]any{
		"PLACEHOLDER": "${HOME}/.tool",
		"RATIO":       "%COMPLETE%",
	}), facts)
	if got.Outcome != TransformKept {
		t.Fatalf("runtime placeholders and bare vars must pass: %+v (%s)", got, got.Reason)
	}
}
