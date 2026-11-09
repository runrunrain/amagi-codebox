package install

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

var update = flag.Bool("update", false, "重写 golden 基准文件")

// goldenTable 是 docgen golden 测试的内置假表（覆盖全部风险级、确认值、
// 参数类型/形态/枚举/默认/stdin/secret、Notes/Timeout/示例与各领域首词）。
// 按任务约定不对真实命令表做 golden——Wave 2 接入真实表时另加防漂移测试。
func goldenTable() []spec.Command {
	return []spec.Command{
		{
			Path:     []string{"status"},
			Summary:  "应用总览：版本、平台能力、会话计数与环境摘要。",
			Risk:     spec.RiskRead,
			Examples: []string{"codebox-agent status --json"},
		},
		{
			Path:    []string{"env", "check"},
			Summary: "实时探测环境（全部或指定工具）。",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "tool", Type: spec.TypeString, Positional: true, Enum: []string{"claudecode", "opencode", "codex", "pi", "omp"}, Desc: "只探测该工具；缺省全部"},
			},
			Examples: []string{"codebox-agent env check claudecode"},
		},
		{
			Path:    []string{"provider", "get"},
			Summary: "查看单个提供商（输出脱敏）。",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "name", Type: spec.TypeString, Required: true, Positional: true, Desc: "提供商名称"},
				{Name: "type", Type: spec.TypeString, Enum: []string{"claudecode", "opencode", "codex", "pi", "omp"}, Desc: "按应用类型消歧"},
			},
		},
		{
			Path:    []string{"provider", "key-set"},
			Summary: "写入提供商 API Key（经 stdin，不回显）。",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{Name: "name", Type: spec.TypeString, Required: true, Positional: true, Desc: "提供商名称"},
				{Name: "key", Type: spec.TypeString, Required: true, Stdin: true, Secret: true, Desc: "密钥值；传 - 从 stdin 读取"},
			},
			Notes:    "密钥不会出现在 argv、日志与回显中。",
			Examples: []string{`printf '%s' "$KEY" | codebox-agent provider key-set myprov -`},
		},
		{
			Path:    []string{"provider", "save"},
			Summary: "保存提供商（完整 JSON）。",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{Name: "json", Type: spec.TypeJSON, Required: true, Desc: "提供商 JSON（同桌面端导入格式）"},
			},
			Notes: "JSON 含 key 字段时会被拒绝，改用 provider key-set。",
		},
		{
			Path:    []string{"session", "launch"},
			Summary: "启动 AI-CLI 会话，返回 sessionId。",
			Risk:    spec.RiskExec,
			Confirm: "LAUNCH",
			Params: []spec.Param{
				{Name: "appType", Type: spec.TypeString, Required: true, Enum: []string{"claudecode", "opencode", "codex", "pi", "omp"}, Desc: "目标 CLI"},
				{Name: "provider", Type: spec.TypeString, Required: true, Desc: "提供商名称"},
				{Name: "model", Type: spec.TypeString, Desc: "模型覆盖"},
				{Name: "workDir", Type: spec.TypeString, Desc: "工作目录"},
				{Name: "mode", Type: spec.TypeString, Default: "embedded", Desc: "会话模式"},
				{Name: "headroom", Type: spec.TypeBool, Default: false, Desc: "启用 Headroom"},
			},
			Timeout:  300,
			Examples: []string{"codebox-agent session launch --app-type claudecode --provider myprov --yes"},
		},
		{
			Path:    []string{"session", "output"},
			Summary: "读取会话输出（默认剥离 ANSI）。",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "id", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"},
				{Name: "tailBytes", Type: spec.TypeInt, Default: 4096, Desc: "只读末尾 N 字节"},
				{Name: "sinceSeq", Type: spec.TypeInt, Desc: "从该序号起增量读取"},
				{Name: "raw", Type: spec.TypeBool, Desc: "保留 ANSI 转义"},
			},
			Notes: "输出可能含敏感信息，注意脱敏。",
		},
		{
			Path:    []string{"session", "send"},
			Summary: "向运行中的会话发送输入。",
			Risk:    spec.RiskExec,
			Confirm: "SEND",
			Params: []spec.Param{
				{Name: "id", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"},
				{Name: "text", Type: spec.TypeStrings, Required: true, Positional: true, Desc: "输入文本（多段合并）"},
				{Name: "enter", Type: spec.TypeBool, Desc: "追加回车"},
			},
		},
		{
			Path:    []string{"session", "stop"},
			Summary: "停止会话。",
			Risk:    spec.RiskDestructive,
			Confirm: "STOP",
			Params:  []spec.Param{{Name: "id", Type: spec.TypeString, Required: true, Positional: true, Desc: "会话 ID"}},
		},
		{
			Path:    []string{"usage", "daily"},
			Summary: "按日汇总用量。",
			Risk:    spec.RiskRead,
			Params:  []spec.Param{{Name: "days", Type: spec.TypeInt, Default: 7, Desc: "统计天数"}},
		},
		{
			Path:    []string{"logs"},
			Summary: "查询应用日志。",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "level", Type: spec.TypeString, Enum: []string{"debug", "info", "warn", "error"}, Desc: "级别过滤"},
				{Name: "limit", Type: spec.TypeInt, Default: 100, Desc: "条数上限"},
			},
		},
		{
			Path:    []string{"git", "repo-info"},
			Summary: "查看仓库状态摘要。",
			Risk:    spec.RiskRead,
			Params:  []spec.Param{{Name: "dir", Type: spec.TypeString, Required: true, Positional: true, Desc: "仓库路径"}},
		},
		{
			Path:    []string{"plugin", "install"},
			Summary: "安装插件。",
			Risk:    spec.RiskExec,
			Confirm: "INSTALL",
			Params: []spec.Param{
				{Name: "app", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "codex", "opencode", "pi", "omp"}, Desc: "插件引擎"},
				{Name: "name", Type: spec.TypeString, Required: true, Positional: true, Desc: "插件名"},
			},
		},
	}
}

func TestGenerateCommandReferenceGolden(t *testing.T) {
	got := GenerateCommandReference(goldenTable())
	goldenPath := filepath.Join("testdata", "command-reference.golden.md")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("读取 golden 失败（先跑 go test ./internal/agentcli/install -update 生成基准后人工复核）：%v", err)
	}
	if string(want) != got {
		t.Errorf("GenerateCommandReference 与 golden 不一致（go test ./internal/agentcli/install -update 重写后人工复核）\n--- got ---\n%s", got)
	}
}

func TestGenerateCommandReferenceEmptyTable(t *testing.T) {
	got := GenerateCommandReference(nil)
	if !strings.Contains(got, "命令表暂为空") {
		t.Fatalf("空表应输出占位说明，got:\n%s", got)
	}
	if strings.Contains(got, "### ") {
		t.Fatal("空表不应有命令小节")
	}
}

func TestGenerateCommandReferenceGroupsAndDetails(t *testing.T) {
	got := GenerateCommandReference(goldenTable())
	// 已知领域分组齐全且有序
	order := []string{"## 通用", "## 系统与状态", "## 配置", "## 会话", "## 用量与日志", "## 插件", "## Git"}
	last := -1
	for _, h := range order {
		i := strings.Index(got, h)
		if i < 0 {
			t.Fatalf("缺少分组标题 %q", h)
		}
		if i < last {
			t.Fatalf("分组顺序错乱：%q 出现在前一个分组之前", h)
		}
		last = i
	}
	// 用法行：位置参数 + kebab 旗标
	if !strings.Contains(got, "codebox-agent provider get <name> [--type <type>]") {
		t.Fatal("用法行应含位置参数与可选旗标")
	}
	if !strings.Contains(got, "codebox-agent session output <id> [--tail-bytes <tailBytes>] [--since-seq <sinceSeq>] [--raw]") {
		t.Fatal("用法行应含 kebab-case 旗标")
	}
	// 风险与确认值
	if !strings.Contains(got, "确认值 `LAUNCH`——执行需 `--yes`") {
		t.Fatal("exec 命令应显示确认值")
	}
	if !strings.Contains(got, "超时 300 秒") {
		t.Fatal("登记了 Timeout 的命令应显示超时")
	}
	// 参数属性
	if !strings.Contains(got, "支持 `-` 从 stdin 读取") || !strings.Contains(got, "敏感值，日志与回显脱敏") {
		t.Fatal("stdin/secret 参数应有标注")
	}
	if !strings.Contains(got, "默认 embedded") || !strings.Contains(got, "默认 4096") {
		t.Fatal("默认值应展示")
	}
}

func TestKebabCase(t *testing.T) {
	cases := map[string]string{
		"buildId":   "build-id",
		"appType":   "app-type",
		"workDir":   "work-dir",
		"sinceSeq":  "since-seq",
		"tailBytes": "tail-bytes",
		"id":        "id",
		"raw":       "raw",
	}
	for in, want := range cases {
		if got := kebabCase(in); got != want {
			t.Errorf("kebabCase(%q) = %q, want %q", in, got, want)
		}
	}
}
