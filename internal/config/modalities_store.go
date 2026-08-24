package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 设备端多模态知识库学习层（契约 docs/vision-export-contract.md v1.3）。
//
// 能力知识分两层：
//  1. 内置规则表（modalities.go，随二进制发布）：主流模型族的保守基线；
//  2. 设备学习层（本文件，~/.agents/amagi-modalities.json）：实弹探测的有定论
//     结论按**模型 id**（跨 provider 泛化）回写——同一模型换 provider 接入时
//     无需重新探测，否定结论同样落库以防对纯文本模型反复实弹。
//
// 推断顺序（InferModelModalities → LookupModelModalities）：学习层精确命中
// （实证，含否定）优先；未命中再走内置族规则。学习层是单用户桌面应用的本地
// 文件：原子写（tmp+rename，0600）、mtime 缓存读、损坏文件按空处理不崩溃。

const (
	// ModalityKBVersion 学习层文件格式版本。
	ModalityKBVersion = 1
	// ModalityKBPathEnv 环境变量：覆盖学习层路径（测试用）。
	ModalityKBPathEnv = "AMAGI_MODALITY_KB_PATH"
	// modalityKBFileName 默认文件名（位于 ~/.agents 下）。
	modalityKBFileName = "amagi-modalities.json"
)

// ModalityKBFile 学习层文件结构（字段名即 API，单方不得擅改）。
type ModalityKBFile struct {
	Version   int                           `json:"version"`
	UpdatedAt string                        `json:"updated_at"`
	Models    map[string]ModalityProbeEntry `json:"models"`
}

// ModalityKBPath 解析学习层文件路径（env 覆盖优先，默认 ~/.agents/amagi-modalities.json）。
func ModalityKBPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv(ModalityKBPathEnv)); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".agents", modalityKBFileName), nil
}

// modalityKBState 进程内学习层缓存：按「路径 + mtime + size」指纹决定是否重读，
// 测试切换 AMAGI_MODALITY_KB_PATH 时天然失效重载，无需显式 reset。
var modalityKBState = struct {
	sync.Mutex
	path    string
	modTime time.Time
	size    int64
	loaded  bool
	entries map[string]ModalityProbeEntry
}{}

// normalizeKBModelID 学习层键规范化：小写 + 剥离 provider/ 前缀（与
// InferModelModalities 的匹配输入同型，保证写入/读取同键）。
func normalizeKBModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	return id
}

// loadModalityKBLocked 读学习层（指纹命中复用进程内缓存）。文件缺失/损坏
// 一律按空表处理——学习层是增强而非依赖，任何 IO 故障不得影响主流程。
// 调用方必须持有 modalityKBState.mu。
func loadModalityKBLocked() map[string]ModalityProbeEntry {
	path, err := ModalityKBPath()
	if err != nil {
		return map[string]ModalityProbeEntry{}
	}
	stat, statErr := os.Stat(path)
	if statErr == nil && modalityKBState.loaded &&
		modalityKBState.path == path && stat.ModTime().Equal(modalityKBState.modTime) && stat.Size() == modalityKBState.size {
		return modalityKBState.entries
	}
	entries := map[string]ModalityProbeEntry{}
	if statErr == nil {
		if data, readErr := os.ReadFile(path); readErr == nil {
			var file ModalityKBFile
			if jsonErr := json.Unmarshal(data, &file); jsonErr == nil && file.Models != nil {
				entries = file.Models
			}
		}
	}
	modalityKBState.path = path
	modalityKBState.loaded = true
	modalityKBState.entries = entries
	if statErr == nil {
		modalityKBState.modTime = stat.ModTime()
		modalityKBState.size = stat.Size()
	} else {
		modalityKBState.modTime = time.Time{}
		modalityKBState.size = -1
	}
	return entries
}

// lookupLearnedModalities 查学习层：命中（含否定结论）返回 (mods, true)。
func lookupLearnedModalities(model string) (ModelModalities, bool) {
	id := normalizeKBModelID(model)
	if id == "" {
		return ModelModalities{}, false
	}
	modalityKBState.Lock()
	entries := loadModalityKBLocked()
	modalityKBState.Unlock()
	entry, ok := entries[id]
	if !ok {
		return ModelModalities{}, false
	}
	return ModelModalities{Vision: entry.Vision, Video: entry.Video}, true
}

// RecordLearnedModalities 把有定论的探测结论回写设备学习层（读-改-写，
// 进程内 mutex 串行化；原子写盘 0600）。写失败返回错误但不致命——调用方
// 记日志即可，学习层丢失最多退化为重复探测。
func RecordLearnedModalities(model, source string, mods ModelModalities) error {
	id := normalizeKBModelID(model)
	if id == "" {
		return errors.New("model id is required")
	}
	modalityKBState.Lock()
	defer modalityKBState.Unlock()
	entries := loadModalityKBLocked()
	// 复制后修改，避免写盘失败污染内存缓存。
	next := make(map[string]ModalityProbeEntry, len(entries)+1)
	for k, v := range entries {
		next[k] = v
	}
	next[id] = ModalityProbeEntry{
		Vision:   mods.Vision,
		Video:    mods.Video,
		Source:   source,
		ProbedAt: time.Now().Format(time.RFC3339),
	}
	file := ModalityKBFile{
		Version:   ModalityKBVersion,
		UpdatedAt: time.Now().Format(time.RFC3339),
		Models:    next,
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal modality kb: %w", err)
	}
	b = append(b, '\n')
	path, err := ModalityKBPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir modality kb dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write modality kb temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace modality kb: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	// 同步内存缓存指纹，避免紧随其后的读取重落盘。
	if stat, statErr := os.Stat(path); statErr == nil {
		modalityKBState.path = path
		modalityKBState.loaded = true
		modalityKBState.entries = next
		modalityKBState.modTime = stat.ModTime()
		modalityKBState.size = stat.Size()
	}
	return nil
}
