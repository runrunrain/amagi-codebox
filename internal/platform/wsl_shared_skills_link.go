package platform

// wsl_shared_skills_link.go — WSL 内共享 Windows ~/.agents/skills（受管符号链接）。
//
// 设计（与 1.3.126「WSL 内 Pi 共享 Windows agent 根」同一裁定：WSL 只提供
// 运行环境，用户资产全部来自 Windows）：pi 原生按 Agent Skills 规范扫描
// $HOME/.agents/skills——distro 内该路径默认是独立目录，Windows 侧技能整体
// 不可见。本模块在 distro 内把 ~/.agents/skills 钉成指向 Windows 技能根
//（wslpath 翻译后的 /mnt/<盘>/… 路径）的符号链接：
//   - 幂等：已指向目标的链接直接返回 already-linked，不重复写；
//   - 既有真实目录/文件（如旧 P3-S3 投影副本）先备份为
//     ~/.agents/skills.pre-shared.<时间戳> 再建链接，不静默覆盖用户内容；
//   - 指向别处的既有符号链接替换为新目标（受管面自愈）；
//   - 写失败如实报告（FAIL-*），不冒充成功。
// 退役说明：P3-S3「共享技能」同意制投影（shared/skill/<name> 单元）随本机制
// 退役——同一棵树不允许两套写入通道（投影副本会遮挡/被符号链接遮挡）。

import (
	"fmt"
	"runtime"
	"strings"
)

// WSLSharedSkillsLinkState 是受管符号链接的终态。
type WSLSharedSkillsLinkState string

const (
	// WSLSkillsLinked 本轮新建了符号链接。
	WSLSkillsLinked WSLSharedSkillsLinkState = "linked"
	// WSLSkillsAlreadyLinked 已存在且指向目标（幂等命中）。
	WSLSkillsAlreadyLinked WSLSharedSkillsLinkState = "already-linked"
	// WSLSkillsBackedUp 既有真实目录/文件已备份迁移并新建链接（Backup 给出备份路径）。
	WSLSkillsBackedUp WSLSharedSkillsLinkState = "backed-up"
	// WSLSkillsReplacedLink 指向别处的既有符号链接已被替换为目标。
	WSLSkillsReplacedLink WSLSharedSkillsLinkState = "replaced-link"
	// WSLSkillsHostMissing Windows 侧技能根不存在或 distro 内不可达——不建
	// 链接（pi 对缺失目录天然容忍），状态面按「宿主无技能根」展示。
	WSLSkillsHostMissing WSLSharedSkillsLinkState = "host-missing"
	// WSLSkillsFailed 写入或探测失败（Detail 给出原因）。
	WSLSkillsFailed WSLSharedSkillsLinkState = "failed"
)

// WSLSharedSkillsLinkResult 是一次供给/核验的结果（展示面与日志共用）。
type WSLSharedSkillsLinkResult struct {
	State   WSLSharedSkillsLinkState
	Target  string // 目标侧 Linux 路径（host-missing/failed 可能为空）
	Link    string // ~/.agents/skills（distro 内落点）
	Backup  string // backed-up 时的备份路径
	Detail  string // 失败原因/备注
}

// wslSharedSkillsScript 在 distro 内核验/修复受管符号链接（事务化：不跟随
// 目标目录的原子发布 + 唯一备份槽 + 终验）。
// 输出行：OK-LINKED / BACKED-UP:<path> / REPLACED-LINK / LINKED /
// FAIL-MKDIR / FAIL-BACKUP-SLOT / FAIL-MOVE / FAIL-MOVE-LOST / FAIL-RM /
// FAIL-LN / FAIL-PUBLISH / FAIL-VERIFY（TARGET-MISSING 为附注行）。
//
// 并发安全（R1-F01/F02、R2-F02）：
//   - 发布不用 `ln -s T L`——L 已是目录符号链接时 ln 会穿透把链接写进目标
//     目录（宿主技能根）内；改为临时名建链后 `mv -T` 原子改名（rename(2)，
//     不解析目标，竞争者已发布时本次替换为同目标链接，幂等收敛）；
//   - 备份槽不覆用：同秒碰撞时追加序号寻空槽，`mv -n` 不覆写已存在目的；
//     迁移成功的证明是「本轮前像已落位」——L 必须已迁离、B 必须存在，且
//     B 为目录时不得再含 skills 条目（目录 L 迁走后 B 即该目录本体；若
//     B 是竞争者目录、L 被控进其内部则 B/skills 存在——一律 FAIL-MOVE-LOST
//     如实报错并停止发布，不拿竞争者内容冒充本轮备份）；
//   - 终验 readlink——任何路径下未确认指向目标一律 FAIL-*，不冒充成功。
func wslSharedSkillsScript(linuxTarget string) string {
	t := wslShQuote(linuxTarget)
	return strings.Join([]string{
		`L="$HOME/.agents/skills"`,
		`T=` + t,
		`if [ -L "$L" ] && [ "$(readlink "$L")" = "$T" ]; then printf 'OK-LINKED\n'; exit 0; fi`,
		`mkdir -p "$HOME/.agents" 2>/dev/null || { printf 'FAIL-MKDIR\n'; exit 1; }`,
		`if [ -e "$L" ] && [ ! -L "$L" ]; then`,
		`  B=""`,
		`  i=0`,
		`  while [ "$i" -lt 50 ]; do`,
		`    C="$HOME/.agents/skills.pre-shared.$(date +%Y%m%d%H%M%S).$i"`,
		`    if [ ! -e "$C" ]; then B="$C"; break; fi`,
		`    i=$((i+1))`,
		`  done`,
		`  if [ -z "$B" ]; then printf 'FAIL-BACKUP-SLOT\n'; exit 1; fi`,
		`  mv -n "$L" "$B" || { printf 'FAIL-MOVE\n'; exit 1; }`,
		`  if [ -e "$L" ] || [ ! -e "$B" ] || { [ -d "$B" ] && [ -e "$B/skills" ]; }; then printf 'FAIL-MOVE-LOST\n'; exit 1; fi`,
		`  printf 'BACKED-UP:%s\n' "$B"`,
		`elif [ -L "$L" ]; then`,
		`  rm -f "$L" || { printf 'FAIL-RM\n'; exit 1; }`,
		`  printf 'REPLACED-LINK\n'`,
		`fi`,
		`N="$HOME/.agents/.skills-link.$$.tmp"`,
		`rm -f "$N"`,
		`ln -s "$T" "$N" || { rm -f "$N"; printf 'FAIL-LN\n'; exit 1; }`,
		`if mv -T "$N" "$L" 2>/dev/null; then`,
		`  :`,
		`else`,
		`  rm -f "$N"`,
		`  if [ -L "$L" ] && [ "$(readlink "$L")" = "$T" ]; then printf 'OK-LINKED\n'; exit 0; fi`,
		`  printf 'FAIL-PUBLISH\n'; exit 1`,
		`fi`,
		`if [ "$(readlink "$L")" = "$T" ]; then`,
		`  if [ ! -d "$T" ]; then printf 'TARGET-MISSING\n'; fi`,
		`  printf 'LINKED\n'`,
		`else`,
		`  printf 'FAIL-VERIFY\n'; exit 1`,
		`fi`,
	}, "\n")
}

// wslShQuote POSIX 单引号包裹（值内单引号转义为 '\'' 形态）。
func wslShQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// WSLEnsureSharedAgentsSkillsLink 核验（并在需要时修复）distro 内
// ~/.agents/skills → Windows hostSkillsDir 的受管符号链接。
// hostSkillsDir 必须是 Windows 绝对路径；distro 为空或非 Windows 平台
// 返回 failed（调用方按「不适用」降级，不重试）。
func WSLEnsureSharedAgentsSkillsLink(distro, hostSkillsDir string) WSLSharedSkillsLinkResult {
	res := WSLSharedSkillsLinkResult{Link: "~/.agents/skills"}
	distro = strings.TrimSpace(distro)
	hostSkillsDir = strings.TrimSpace(hostSkillsDir)
	if distro == "" || hostSkillsDir == "" || len(hostSkillsDir) < 3 || hostSkillsDir[1] != ':' {
		res.State = WSLSkillsFailed
		res.Detail = "参数不完整（distro/Windows 技能根缺失）"
		return res
	}
	if runtime.GOOS != "windows" {
		res.State = WSLSkillsFailed
		res.Detail = "非 Windows 宿主不适用"
		return res
	}
	// 目标路径须经 distro 内 wslpath 实测翻译；执行失败（distro 不可达等）与
	//「确认目标不存在」分开报告（R1-F07：不把运行故障冒充宿主缺失）。
	mapped, terr := wslTranslatePathsCore(distro, hostSkillsDir)
	linuxTarget := mapped[normalizeWindowsPathKey(hostSkillsDir)]
	if terr != nil {
		res.State = WSLSkillsFailed
		res.Detail = fmt.Sprintf("路径翻译探测失败：%v", terr)
		return res
	}
	if linuxTarget == "" {
		res.State = WSLSkillsHostMissing
		res.Detail = "Windows 技能根在 distro 内不可达（不存在或未挂载）"
		return res
	}
	res.Target = linuxTarget
	out, err := wslScriptRunner(distro, wslSharedSkillsScript(linuxTarget))
	if err != nil {
		res.State = WSLSkillsFailed
		res.Detail = fmt.Sprintf("distro 内执行失败：%v", err)
		return res
	}
	return parseWSLSharedSkillsLinkResult(decodeWSLListOutput(out), linuxTarget)
}

// parseWSLSharedSkillsLinkResult 解析 distro 内脚本的终态行（纯函数，跨平台
// 可测）。非确认形态一律 failed，不冒充成功。
func parseWSLSharedSkillsLinkResult(out, linuxTarget string) WSLSharedSkillsLinkResult {
	res := WSLSharedSkillsLinkResult{Link: "~/.agents/skills", Target: linuxTarget}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := ""
	backup := ""
	replaced := false
	targetMissing := false
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		last = l
		if strings.HasPrefix(l, "BACKED-UP:") {
			backup = strings.TrimPrefix(l, "BACKED-UP:")
		}
		if l == "REPLACED-LINK" {
			replaced = true
		}
		if l == "TARGET-MISSING" {
			targetMissing = true
		}
	}
	// 附注行（BACKED-UP/REPLACED-LINK/TARGET-MISSING）之后脚本总会输出终行
	//（LINKED=成功）；附注携带更丰富的迁移事实，优先于裸 LINKED 报告。
	switch {
	case last == "OK-LINKED":
		res.State = WSLSkillsAlreadyLinked
		res.Detail = "已指向 Windows 技能根"
	case last == "LINKED" && backup != "":
		res.State = WSLSkillsBackedUp
		res.Backup = backup
		res.Detail = "既有目录已备份迁移：" + res.Backup
	case last == "LINKED" && replaced:
		res.State = WSLSkillsReplacedLink
		res.Detail = "已替换指向别处的旧符号链接"
	case last == "LINKED":
		res.State = WSLSkillsLinked
		res.Detail = "已建立指向 Windows 技能根的符号链接"
		if targetMissing {
			res.Detail += "（注意：目标目录当前不存在）"
		}
	default:
		res.State = WSLSkillsFailed
		res.Detail = "distro 内脚本未确认成功：" + last
	}
	return res
}
