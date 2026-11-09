package install

// docs_up_to_date_test.go — docs/user/agent-cli.md 生成区防漂移测试（C2）：
// 生成区必须与 install.GenerateCommandReference(cmds.Table()) 逐字节一致
// （命令表是唯一事实源，不维护第二份手写命令清单）。

import (
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/agentcli/cmds"
)

// docsPath 从包目录（internal/agentcli/install）指向仓库根的用户文档。
var docsPath = filepath.Join("..", "..", "..", "docs", "user", "agent-cli.md")

func TestAgentCLIDocsUpToDate(t *testing.T) {
	want := GenerateCommandReference(cmds.Table())
	if *update {
		if _, err := os.Stat(docsPath); err != nil {
			t.Fatalf("读取 %s 失败：%v（生成区标记缺失时请先补齐 BEGIN/END 标记行）", docsPath, err)
		}
		if err := UpdateDocsGeneratedRegion(docsPath, cmds.Table()); err != nil {
			t.Fatalf("重写生成区失败：%v", err)
		}
		return
	}
	data, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", docsPath, err)
	}
	got, ok := ExtractDocsGeneratedRegion(string(data))
	if !ok {
		t.Fatalf("%s 缺少或损坏的 BEGIN/END 生成区标记（%s / %s）；请勿手改生成区", docsPath, DocsBeginMarker, DocsEndMarker)
	}
	if got != want {
		t.Errorf("docs/user/agent-cli.md 生成区与命令表不一致。命令表变更后请运行：\n  go test ./internal/agentcli/install -run TestAgentCLIDocsUpToDate -update\n重生成后人工复核。")
	}
}
