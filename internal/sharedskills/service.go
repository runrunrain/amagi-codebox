// Package sharedskills 管理 ~/.agents/skills 与 ~/.agents/skills-disabled 下的
// 共用技能目录：枚举、SKILL.md frontmatter 元数据解析、启停（目录移动）与
// 删除（扩展中心重构 spec §2.1）。
//
// 设计边界：本包只面向 ~/.agents 的共用技能，不依赖
// internal/agentcli/install（amagi-codebox 自带技能的 managed 状态由 App 门面
// 组装，见 app_sharedskills.go），也不依赖 internal/wslsupply（那是 WSL 供给
// 扫描，语义不同）。
package sharedskills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"amagi-codebox/internal/logging"
)

// ManagedSkillName 是 amagi-codebox 自带技能的目录名（与
// internal/agentcli/install 的 skillName 字面一致，spec §2.4）。本包不反向
// import install（避免包环）；managed 技能的启停/删除拦截在 App 门面层执行。
const ManagedSkillName = "amagi-codebox"

// Skill 是一个共用技能目录的视图（目录名是稳定标识）。
type Skill struct {
	Name        string `json:"name"`        // 目录名（标识，basename 校验过）
	DisplayName string `json:"displayName"` // frontmatter name，缺失回退目录名
	Description string `json:"description"`
	Version     string `json:"version"`    // frontmatter metadata.version
	Author      string `json:"author"`     // frontmatter metadata.author
	Enabled     bool   `json:"enabled"`    // 位于 skills/ → true；skills-disabled/ → false
	Path        string `json:"path"`       // 绝对路径
	HasSkillMD  bool   `json:"hasSkillMd"` // SKILL.md 缺失时元数据为回退值并如实标记
}

// Service 是共用技能的枚举/启停/删除服务（无缓存，每次调用实时扫描）。
type Service struct {
	userHome string
	log      *logging.Service
}

// New 创建服务。userHome 是用户主目录（~/.agents 所在）；log 允许为 nil
// （测试夹具），记录点自行判空。
func New(userHome string, log *logging.Service) *Service {
	return &Service{userHome: userHome, log: log}
}

// SkillsRoot 返回启用态根目录 ~/.agents/skills（供「打开技能根目录」门面）。
func (s *Service) SkillsRoot() string {
	return filepath.Join(s.userHome, ".agents", "skills")
}

// disabledRoot 返回禁用态根目录 ~/.agents/skills-disabled。
func (s *Service) disabledRoot() string {
	return filepath.Join(s.userHome, ".agents", "skills-disabled")
}

// renameDir 指向 os.Rename；测试可注入失败以验证回滚（同 logging 包
// reportWriteError 的可覆写 var 先例）。
var renameDir = os.Rename

// validName 校验技能目录名：非空、非 "."/".."、非隐藏目录、单段（不含路径
// 分隔符）。防路径穿越与误操作（对齐 internal/agentcli/install 的边界检查
// 思路，spec §2.1）。
func validName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.HasPrefix(name, ".") &&
		filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\`)
}

// locate 定位 name 当前所在的目录：返回绝对路径与启用标志（启用根优先）。
// 校验目标必须是普通目录（os.Lstat 为 IsDir 且非符号链接/junction——防误
// 操作链接目标）。未找到 → 明确中文错误；同名同时存在于两个根（手动复制
// 造成的异常态）时以启用根为准。
func (s *Service) locate(name string) (dir string, enabled bool, err error) {
	for _, cand := range []struct {
		root    string
		enabled bool
	}{{s.SkillsRoot(), true}, {s.disabledRoot(), false}} {
		p := filepath.Join(cand.root, name)
		fi, lerr := os.Lstat(p)
		if lerr != nil {
			if errors.Is(lerr, fs.ErrNotExist) {
				continue
			}
			return "", false, fmt.Errorf("读取目录信息失败：%s（%v）", p, lerr)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return "", false, fmt.Errorf("%s 不是普通目录（可能是符号链接），已拒绝操作", p)
		}
		return p, cand.enabled, nil
	}
	return "", false, fmt.Errorf("技能 %q 不存在（已同时检查 ~/.agents/skills 与 ~/.agents/skills-disabled）", name)
}

// inspect 组装单个技能目录的视图（宽容元数据解析，见 skillmd.go）。
func (s *Service) inspect(name, root string, enabled bool) Skill {
	dir := filepath.Join(root, name)
	sk := Skill{Name: name, DisplayName: name, Enabled: enabled, Path: dir}
	meta, hasMD, perr := readSkillMetadata(dir)
	sk.HasSkillMD = hasMD
	if meta.Name != "" {
		sk.DisplayName = meta.Name
	}
	sk.Description = meta.Description
	sk.Version = meta.Version
	sk.Author = meta.Author
	if perr != nil && s.log != nil {
		// YAML 坏但文件在 → HasSkillMD=true + 元数据回退；解析错误记 Debug，
		// 不向用户报错（spec §2.3 宽容规则）。
		s.log.Debug("sharedskills", "SKILL.md 元数据解析失败，使用回退值", "skill="+name, "err="+perr.Error())
	}
	return sk
}

// List 扫描 ~/.agents/skills 与 ~/.agents/skills-disabled 两个根的顶层非隐藏
// 目录（对齐 wslsupply listSkillDirs 的取舍：隐藏目录/文件不列），合并按
// Name 排序。两个根目录都不存在 → 返回空切片、不报错（首次使用即空态）。
// 同名目录同时出现在两个根（手动复制造成的异常态）时只列启用根的条目，
// 启停时目标同名冲突会被 SetEnabled 如实拒绝。
func (s *Service) List() ([]Skill, error) {
	seen := map[string]bool{}
	var out []Skill
	for _, cand := range []struct {
		root    string
		enabled bool
	}{{s.SkillsRoot(), true}, {s.disabledRoot(), false}} {
		entries, err := os.ReadDir(cand.root)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("读取技能目录失败：%s（%v）", cand.root, err)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if seen[e.Name()] {
				if s.log != nil {
					s.log.Debug("sharedskills", "同名技能同时存在于启用/禁用根，仅列启用根条目", "skill="+e.Name())
				}
				continue
			}
			seen[e.Name()] = true
			out = append(out, s.inspect(e.Name(), cand.root, cand.enabled))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if out == nil {
		out = []Skill{}
	}
	return out, nil
}

// Detail 返回单个技能的视图；不存在或名字非法 → 明确中文 error（供前端
// 区分 not-found）。
func (s *Service) Detail(name string) (Skill, error) {
	if !validName(name) {
		return Skill{}, fmt.Errorf("无效的技能名 %q：不允许为空、隐藏目录或包含路径分隔符", name)
	}
	dir, enabled, err := s.locate(name)
	if err != nil {
		return Skill{}, err
	}
	return s.inspect(name, filepath.Dir(dir), enabled), nil
}

// SetEnabled 启用/停用技能：enabled=false 时把 ~/.agents/skills/<name> 移入
// ~/.agents/skills-disabled/<name>；true 时反向（spec §2.2）。对真实读取
// ~/.agents/skills 的 CLI 立即生效。已处于目标状态时幂等返回当前视图。
// 失败回滚：仅当本次新建了目标根且其为空目录时尽力 os.Remove（失败仅记
// 日志，不掩盖原错误）。
func (s *Service) SetEnabled(name string, enabled bool) (Skill, error) {
	if !validName(name) {
		return Skill{}, fmt.Errorf("无效的技能名 %q：不允许为空、隐藏目录或包含路径分隔符", name)
	}
	src, cur, err := s.locate(name)
	if err != nil {
		return Skill{}, err
	}
	if cur == enabled {
		return s.inspect(name, filepath.Dir(src), cur), nil
	}
	dstRoot := s.disabledRoot()
	if enabled {
		dstRoot = s.SkillsRoot()
	}
	dst := filepath.Join(dstRoot, name)

	// 目标根不存在则创建（失败 → 返回错误，此时无任何移动发生）。
	_, rerr := os.Lstat(dstRoot)
	rootMissing := errors.Is(rerr, fs.ErrNotExist)
	if rerr != nil && !rootMissing {
		return Skill{}, fmt.Errorf("检查目录失败：%s（%v）", dstRoot, rerr)
	}
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return Skill{}, fmt.Errorf("创建目录失败：%s（%v）", dstRoot, err)
	}

	// 目标同名已存在 → 明确拒绝（Unix Rename 会静默覆盖空目录，须前置检查）。
	if _, derr := os.Lstat(dst); derr == nil {
		return Skill{}, fmt.Errorf("目标目录已存在：%s，请手动处理后再试", dst)
	} else if !errors.Is(derr, fs.ErrNotExist) {
		return Skill{}, fmt.Errorf("检查目标目录失败：%s（%v）", dst, derr)
	}

	if err := renameDir(src, dst); err != nil {
		// 回滚：本次新建的目标根若仍为空目录则尽力删除（os.Remove 对非空
		// 目录天然失败，失败仅记日志，不掩盖原错误）。
		if rootMissing {
			if rmErr := os.Remove(dstRoot); rmErr != nil && s.log != nil {
				s.log.Debug("sharedskills", "回滚清理新建目录失败（忽略）", "dir="+dstRoot, "err="+rmErr.Error())
			}
		}
		return Skill{}, fmt.Errorf("移动技能目录失败：%s → %s（%v）", src, dst, err)
	}
	return s.inspect(name, dstRoot, enabled), nil
}

// Delete 永久删除技能目录（os.RemoveAll，含禁用态目录；locate 已校验为普通
// 目录，不穿越符号链接）。managed 技能不在本包拦截（由 App 门面层拒绝）。
func (s *Service) Delete(name string) error {
	if !validName(name) {
		return fmt.Errorf("无效的技能名 %q：不允许为空、隐藏目录或包含路径分隔符", name)
	}
	dir, _, err := s.locate(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除技能目录失败：%s（%v）", dir, err)
	}
	return nil
}

// SkillDir 返回技能当前所在绝对路径（供「打开目录」门面）。
func (s *Service) SkillDir(name string) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("无效的技能名 %q：不允许为空、隐藏目录或包含路径分隔符", name)
	}
	dir, _, err := s.locate(name)
	if err != nil {
		return "", err
	}
	return dir, nil
}
