package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain 隔离设备端学习层：InferModelModalities 链路会读学习层文件，统一指到
// 临时目录——防真实 ~/.agents/amagi-modalities.json 污染断言，也防测试写真机。
func TestMain(m *testing.M) {
	if dir, err := os.MkdirTemp("", "amagi-modality-kb-test"); err == nil {
		os.Setenv(ModalityKBPathEnv, filepath.Join(dir, "kb.json"))
	}
	os.Exit(m.Run())
}
