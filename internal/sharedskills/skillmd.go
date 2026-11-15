// skillmd.go — SKILL.md frontmatter 元数据解析（spec §2.3）。
//
// 宽容规则：无 SKILL.md / 无 frontmatter / YAML 解析失败 / 字段缺失 → 一律
// 不报错：DisplayName 回退目录名、其余字段空串、HasSkillMD 如实标记（文件
// 在但解析坏 → true + 元数据回退）。「用户可见状态必须真实」：不伪造版本号。

package sharedskills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// skillMDLimit 是 SKILL.md 的有界读取上限（对齐 wslsupply readFileBounded
// 的有界读取思想，防异常大文件拖垮扫描）。
const skillMDLimit = 64 << 10 // 64KB

// skillMeta 是从 frontmatter 提取的元数据（Name 缺失时由调用方回退目录名）。
type skillMeta struct {
	Name        string
	Description string
	Version     string
	Author      string
}

// frontmatter 对应 SKILL.md 头部 YAML 块的结构（yaml.v3 为 go.mod 既有直接
// 依赖，不引新依赖）。
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		Version string `yaml:"version"`
		Author  string `yaml:"author"`
	} `yaml:"metadata"`
}

// readSkillMetadata 读取 <dir>/SKILL.md 前 64KB 并解析 frontmatter。返回
// （元数据, 文件是否存在, 解析错误）。解析错误不阻断调用方：由调用方记
// Debug 日志并使用回退值；文件不存在时 hasSkillMD=false 且无错误。
func readSkillMetadata(dir string) (m skillMeta, hasSkillMD bool, parseErr error) {
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return m, false, nil
		}
		// 存在但打不开（权限等）：如实标记文件在，元数据回退。
		return m, true, fmt.Errorf("读取 SKILL.md 失败：%w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, skillMDLimit))
	if err != nil {
		return m, true, fmt.Errorf("读取 SKILL.md 失败：%w", err)
	}
	block, ok := extractFrontmatter(string(data))
	if !ok {
		return m, true, nil // 无 frontmatter：不报错，元数据回退
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return m, true, fmt.Errorf("frontmatter 解析失败：%w", err)
	}
	return skillMeta{
		Name:        fm.Name,
		Description: fm.Description,
		Version:     fm.Metadata.Version,
		Author:      fm.Metadata.Author,
	}, true, nil
}

// extractFrontmatter 提取首行 `---` 与下一个独立行 `---` 之间的 frontmatter
// 块。无闭合 `---`（含 64KB 截断导致块不完整的情形）按缺失处理，不报错。
func extractFrontmatter(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return "", false
	}
	var block []string
	for _, ln := range lines[1:] {
		if strings.TrimRight(ln, "\r") == "---" {
			return strings.Join(block, "\n"), true
		}
		block = append(block, ln)
	}
	return "", false
}
