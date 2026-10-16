package piplugin

// target.go — P3-S5 插件操作目标化接缝：把本包私有的源解析语义以只读形式
// 暴露给 WSL 供给协调器（internal/wslsupply），并提供 Windows 侧实体锚点
// 读取（npm 实体 package.json 的 version / git 实体 .git/HEAD 的 commit）。
//
// 设计依据（p3-config-supply §四「插件实体：复制还是重放」）：
//   - WSL 侧重放不复制 node_modules/git 实体，走目标原生 CLI；
//   - npm 源带确切版本、git 源固定 commit ref——「同时应用」模式由宿主侧
//     已解析的实体版本/commit 作为重放锚点（宿主一次 update 不等价为目标
//     再次追 latest）；
//   - 本文件只做解析与只读探测，不执行任何命令、不写任何文件。

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
)

// SourceSpec 是 parseSource 的导出投影（WSL 重放需要 type/name/ref/host/path
// 语义与 pi CLI 自身一致——直接复用本包解析器，不另造一套）。
type SourceSpec struct {
	// Type: npm / git / local。
	Type string
	// Name 是 npm 包名（含 scope、不含版本）。
	Name string
	// Ref 是 npm 版本或 git ref（可为空 = latest / 默认分支）。
	Ref string
	// Host / Path 是 git 源的 host 与 user/project（path 已去 .git 与 @ref）。
	Host string
	Path string
	// LocalPath 是 local 源的原始路径。
	LocalPath string
}

// ParseSourceSpec 把包源字符串解析为重放级描述。解析语义与 pi CLI 的
// parseSource 一致（见 source.go）；本函数只是导出包装，规则变化只发生在一处。
func ParseSourceSpec(raw string) SourceSpec {
	parsed := parseSource(raw)
	return SourceSpec{
		Type:      parsed.sourceType,
		Name:      parsed.name,
		Ref:       parsed.ref,
		Host:      parsed.host,
		Path:      parsed.path,
		LocalPath: parsed.localPath,
	}
}

// IsExactNpmVersion 报告 ref 是否为严格语义化精确版本（决定 npm 重放能否
// 直接视为已固定）。
func IsExactNpmVersion(ref string) bool { return isExactSemver(ref) }

// IsFullCommitSHA 报告 ref 是否为完整 commit SHA（40 位 hex；64 位 SHA-256
// 对象同样接受——git checkout 对两者等价）。
func IsFullCommitSHA(ref string) bool {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ref), "git:"))
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return false
	}
	return true
}

// ResolvedEntityVersion 返回 Windows 侧 npm 实体的实际版本（实体 package.json
// 的 version）。git/local 源或实体缺失/不可解析时返回空串——调用方按
// 「锚点不可得」处理，回退独立解析。
func (s *Service) ResolvedEntityVersion(source string) string {
	spec := parseSource(source)
	if spec.sourceType != sourceNPM || spec.name == "" {
		return ""
	}
	root, _ := s.resolveInstallPath(source)
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	version := gjson.GetBytes(data, "version").String()
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	return ""
}

// ResolvedEntityCommit 返回 Windows 侧 git 实体当前 HEAD commit（只读解析
// .git/HEAD 与 refs，不 spawn git）。非 git 源、实体缺失或 ref 无法解析时
// 返回空串。
func (s *Service) ResolvedEntityCommit(source string) string {
	spec := parseSource(source)
	if spec.sourceType != sourceGit || spec.host == "" || spec.path == "" {
		return ""
	}
	root, _ := s.resolveInstallPath(source)
	if root == "" {
		return ""
	}
	return readGitHeadCommit(filepath.Join(root, ".git"))
}

// WindowsEntryFilters 返回 Windows settings.json 中该源登记的子资源过滤清单
// （packages[] 元素为过滤对象时携带）。重放登记把它记录在案（本批不在 WSL
// settings 中重建过滤对象——如实登记，不冒充已同步）。
func (s *Service) WindowsEntryFilters(source string) (extensions, skills, prompts, themes []string) {
	configured, err := s.readConfiguredPackages()
	if err != nil {
		return nil, nil, nil, nil
	}
	registered := s.findRegistered(source, configured)
	if registered == "" {
		return nil, nil, nil, nil
	}
	for _, cfg := range configured {
		if cfg.source == registered {
			return cfg.extensions, cfg.skills, cfg.prompts, cfg.themes
		}
	}
	return nil, nil, nil, nil
}

// readGitHeadCommit 只读解析 git 仓库 HEAD： detached（HEAD 直接是 SHA）或
// symbolic ref（ref: refs/heads/x → .git/refs/heads/x，缺失时查 packed-refs）。
func readGitHeadCommit(dotGit string) string {
	data, err := os.ReadFile(filepath.Join(dotGit, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if sha, ok := trimCommitSHA(head); ok {
		return sha
	}
	const refPrefix = "ref: "
	if !strings.HasPrefix(head, refPrefix) {
		return ""
	}
	refName := strings.TrimSpace(strings.TrimPrefix(head, refPrefix))
	if refName == "" {
		return ""
	}
	loose, err := os.ReadFile(filepath.Join(dotGit, filepath.FromSlash(refName)))
	if err == nil {
		if sha, ok := trimCommitSHA(strings.TrimSpace(string(loose))); ok {
			return sha
		}
	}
	packed, err := os.ReadFile(filepath.Join(dotGit, "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(packed), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		sha, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if strings.TrimSpace(rest) == refName {
			if v, ok := trimCommitSHA(sha); ok {
				return v
			}
		}
	}
	return ""
}

// trimCommitSHA 校验并归一一段 SHA 文本（40/64 hex）。
func trimCommitSHA(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 40 && len(s) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return s, true
}
