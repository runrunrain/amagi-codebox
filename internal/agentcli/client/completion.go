package client

// completion.go — 由命令表本地生成 bash/zsh/fish 补全脚本（不连服务）。
// 词面仅允许 [a-z0-9][a-z0-9_-]*，其余一律过滤，杜绝注入面。

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
)

var completionWord = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// completionFuncName 是补全函数名（codebox-agent → _codebox_agent）。
func completionFuncName() string {
	return "_" + strings.ReplaceAll(progName, "-", "_")
}

// safeCompletionWords 过滤掉不满足词面约束的词。
func safeCompletionWords(ws []string) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		if completionWord.MatchString(w) {
			out = append(out, w)
		}
	}
	return out
}

// sanitizeCompletionDesc 清理描述中会破坏脚本语法的字符。
func sanitizeCompletionDesc(s string) string {
	r := strings.NewReplacer(
		":", "-", "[", "(", "]", ")", "`", "'", "\"", "", "$", "", "\\", "", "\n", " ",
	)
	return r.Replace(s)
}

// renderCompletion 生成指定 shell 的补全脚本（shell 已由调用方校验）。
func renderCompletion(w io.Writer, shell string, table []spec.Command) {
	switch shell {
	case "bash":
		renderBashCompletion(w, table)
	case "zsh":
		renderZshCompletion(w, table)
	case "fish":
		renderFishCompletion(w, table)
	}
}

func groupSecondWords(cmds []spec.Command) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cmds {
		if len(c.Path) >= 2 && !seen[c.Path[1]] {
			seen[c.Path[1]] = true
			out = append(out, c.Path[1])
		}
	}
	return safeCompletionWords(out)
}

const globalFlagWords = "--json --yes -y --home --timeout --help -h --version"

func renderBashCompletion(w io.Writer, table []spec.Command) {
	groups := groupCommands(table)
	var groupNames []string
	for _, g := range groups {
		if completionWord.MatchString(g.name) {
			groupNames = append(groupNames, g.name)
		}
	}
	fmt.Fprintf(w, "# %s bash 补全（由命令表自动生成；本地数据，不连服务）\n", progName)
	fmt.Fprintf(w, "%s() {\n", completionFuncName())
	fmt.Fprintf(w, `  local cur="${COMP_WORDS[COMP_CWORD]}"
  local prefix="" i=1 w
  while [ "$i" -lt "$COMP_CWORD" ]; do
    w="${COMP_WORDS[$i]}"
    case "$w" in
      -*|help|completion) ;;
      *) prefix="${prefix}${w} " ;;
    esac
    i=$((i+1))
  done
`)
	fmt.Fprintf(w, "  case \"${prefix%% }\" in\n")
	for _, g := range groups {
		if !completionWord.MatchString(g.name) {
			continue
		}
		sub := strings.Join(groupSecondWords(g.cmds), " ")
		fmt.Fprintf(w, "    %s) COMPREPLY=( $(compgen -W \"%s %s\" -- \"$cur\") ); return ;;\n",
			g.name, sub, globalFlagWords)
	}
	fmt.Fprintf(w, "  esac\n")
	fmt.Fprintf(w, "  COMPREPLY=( $(compgen -W \"%s %s help completion\" -- \"$cur\") )\n",
		strings.Join(groupNames, " "), globalFlagWords)
	fmt.Fprintf(w, "}\ncomplete -F %s %s\n", completionFuncName(), progName)
}

func renderZshCompletion(w io.Writer, table []spec.Command) {
	fmt.Fprintf(w, "#compdef %s\n", progName)
	fmt.Fprintf(w, "# %s zsh 补全（由命令表自动生成；本地数据，不连服务）\n", progName)
	fmt.Fprintf(w, "%s() {\n", completionFuncName())
	fmt.Fprintf(w, "  local -a cmds\n  cmds=(\n")
	groups := groupCommands(table)
	for _, g := range groups {
		if !completionWord.MatchString(g.name) {
			continue
		}
		summary := "命令组 " + g.name
		if len(g.cmds) == 1 && len(g.cmds[0].Path) == 1 {
			summary = g.cmds[0].Summary
		}
		fmt.Fprintf(w, "    '%s:%s'\n", g.name, sanitizeCompletionDesc(summary))
	}
	fmt.Fprintf(w, "  )\n")
	fmt.Fprintf(w, `  _arguments -C \
    '--json[以 JSON 包络输出]' \
    '--yes[确认执行]' '-y[确认执行]' \
    '--home[配置目录]:目录' \
    '--timeout[命令超时秒数]:秒' \
    '--help[帮助]' '-h[帮助]' \
    '--version[版本]' \
    '1:命令:->cmds'
  case $state in
    cmds) _describe -t commands '命令' cmds ;;
  esac
}
`)
	fmt.Fprintf(w, "compdef %s %s\n", completionFuncName(), progName)
}

func renderFishCompletion(w io.Writer, table []spec.Command) {
	fmt.Fprintf(w, "# %s fish 补全（由命令表自动生成；本地数据，不连服务）\n", progName)
	fmt.Fprintf(w, "complete -c %s -f\n", progName)
	fmt.Fprintf(w, "complete -c %s -l json -d '以 JSON 包络输出'\n", progName)
	fmt.Fprintf(w, "complete -c %s -l yes -s y -d '确认执行'\n", progName)
	fmt.Fprintf(w, "complete -c %s -l home -r -d '配置目录'\n", progName)
	fmt.Fprintf(w, "complete -c %s -l timeout -r -d '命令超时秒数'\n", progName)
	fmt.Fprintf(w, "complete -c %s -l help -s h -d '帮助'\n", progName)
	fmt.Fprintf(w, "complete -c %s -l version -d '版本'\n", progName)
	groups := groupCommands(table)
	for _, g := range groups {
		if !completionWord.MatchString(g.name) {
			continue
		}
		summary := "命令组 " + g.name
		if len(g.cmds) == 1 && len(g.cmds[0].Path) == 1 {
			summary = g.cmds[0].Summary
		}
		fmt.Fprintf(w, "complete -c %s -n '__fish_use_subcommand' -a '%s' -d '%s'\n",
			progName, g.name, sanitizeCompletionDesc(summary))
		sub := groupSecondWords(g.cmds)
		if len(sub) > 0 {
			fmt.Fprintf(w, "complete -c %s -n '__fish_seen_subcommand_from %s' -a '%s'\n",
				progName, g.name, strings.Join(sub, " "))
		}
	}
}
