package sharedext

// frontmatter.go — SKILL.md frontmatter 解析（复刻 internal/plugin/reader.go
// 的包私有实现，本包不可 import 对方私有函数，故独立维护同语义版本）。

import (
	"strings"
)

// parseFrontmatter 解析 Markdown frontmatter（--- 包围的 `key: value` 块）。
// 坏 frontmatter（无闭合 ---、非 map 形态行）宽容跳过，返回已收集的键值。
func parseFrontmatter(content string) map[string]string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]string{}
	}
	frontmatterEnd := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			frontmatterEnd = i
			break
		}
	}
	if frontmatterEnd == -1 {
		return map[string]string{} // 无闭合 ---
	}

	meta := map[string]string{}
	for i := 1; i < frontmatterEnd; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue // 非 map 形态行：跳过
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		meta[key] = value
	}
	return meta
}

// extractFirstParagraph 提取正文（frontmatter 之后）的首个自然段，
// 作为 description 的回退值。
func extractFirstParagraph(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	// 跳过 frontmatter
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				lines = lines[i+1:]
				break
			}
		}
	}

	paragraph := make([]string, 0)
	inCodeBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}
		if trimmed == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, trimmed)
	}
	return strings.Join(paragraph, " ")
}
