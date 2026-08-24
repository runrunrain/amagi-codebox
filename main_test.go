package main

import (
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/config"
)

// TestMain 隔离设备端学习层（config.ModalityKBPathEnv），理由同 config 包：
// provider_harness_sync 等链路会经 ManagedPresetModels 触及能力推断。
func TestMain(m *testing.M) {
	if dir, err := os.MkdirTemp("", "amagi-modality-kb-test"); err == nil {
		os.Setenv(config.ModalityKBPathEnv, filepath.Join(dir, "kb.json"))
	}
	os.Exit(m.Run())
}
