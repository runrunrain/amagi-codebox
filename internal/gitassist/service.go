// Package gitassist 提供终端会话工作区的 AI 辅助 Git 提交/推送后端：
// 对 session.workDir 所在 git 仓库做状态查询、分支切换、AI 总结变更生成
// 提交信息、提交与推送。AI 用的模型由用户在设置页从已有终端预设中选择
// （settings.CommitSummaryPreset，格式 "provider/preset名"）。
//
// 全部 git 操作通过 exec.CommandContext("git", "-C", workDir, ...) 执行，
// 禁止 shell 拼接；参数独立传递，天然免疫注入。
package gitassist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/settings"
)

// gitCommandTimeout 常规 git 操作超时；push 走 pushTimeout（网络往返更长）。
const (
	gitCommandTimeout = 30 * time.Second
	pushTimeout       = 2 * time.Minute
	// llmTimeout AI 请求超时（长推理模型可能较慢）。
	llmTimeout = 120 * time.Second
	// maxDiffBytes 完整 diff 的截断上限（约 100KB），超出部分以截断标记结尾。
	maxDiffBytes = 100 * 1024
	// truncationMarker diff 截断时追加的标记。
	truncationMarker = "\n...(截断)"
)

// Service 是 AI 辅助 Git 提交/推送服务。cfg 与 settings 由 App 注入；
// apiKeyResolver 把 provider 名解析为明文 API key（App 组装时由
// SecretsService.GetAPIKey 适配注入，参照 vision 导出的 resolver 模式）。
type Service struct {
	cfg            *config.ConfigService
	settings       *settings.Service
	apiKeyResolver func(string) string
}

// New 构造 gitassist 服务。
func New(cfg *config.ConfigService, settings *settings.Service, apiKeyResolver func(string) string) *Service {
	return &Service{cfg: cfg, settings: settings, apiKeyResolver: apiKeyResolver}
}

// RepoStatus 仓库状态快照（前端弹窗展示用）。非 git 仓库时仅 IsGitRepo=false。
type RepoStatus struct {
	IsGitRepo bool   `json:"isGitRepo"`
	Branch    string `json:"branch"`    // 当前分支（detached HEAD 时为空）
	Upstream  string `json:"upstream"`  // 如 "origin/main"；无上游为空
	Ahead     int    `json:"ahead"`     // 领先上游的提交数
	Behind    int    `json:"behind"`    // 落后上游的提交数
	Staged    int    `json:"staged"`    // 已暂存文件数
	Unstaged  int    `json:"unstaged"`  // 未暂存修改文件数
	Untracked int    `json:"untracked"` // 未跟踪文件数
	RemoteURL string `json:"remoteUrl"` // origin 远端地址；无 origin 为空
}

// BranchInfo 分支条目。
type BranchInfo struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

// runGit 在 workDir 执行 git 子命令（args 不含 "git" 与 "-C workDir"），
// 返回 stdout。失败时错误信息附带 stderr，便于直接展示给用户。
func runGit(workDir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workDir}, args...)...)
	platform.SuppressConsoleWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s 失败: %s", args[0], detail)
	}
	return stdout.String(), nil
}

// RepoInfo 汇总 workDir 所在仓库状态。非 git 仓库返回 IsGitRepo=false 而非 error。
func (s *Service) RepoInfo(workDir string) (RepoStatus, error) {
	out, err := runGit(workDir, gitCommandTimeout, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return RepoStatus{IsGitRepo: false}, nil
	}
	status := RepoStatus{IsGitRepo: true}

	if branch, err := runGit(workDir, gitCommandTimeout, "branch", "--show-current"); err == nil {
		status.Branch = strings.TrimSpace(branch)
	}

	// 上游与 ahead/behind：无上游（新分支/未 push）时保持零值不报错。
	if upstream, err := runGit(workDir, gitCommandTimeout, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		fields := strings.Fields(strings.TrimSpace(upstream))
		if len(fields) == 2 {
			// left = 仅上游有的提交（behind），right = 仅本地有的提交（ahead）。
			fmt.Sscanf(fields[0], "%d", &status.Behind)
			fmt.Sscanf(fields[1], "%d", &status.Ahead)
			if upstreamRef, err := runGit(workDir, gitCommandTimeout, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
				status.Upstream = strings.TrimSpace(upstreamRef)
			}
		}
	}

	porcelain, err := runGit(workDir, gitCommandTimeout, "status", "--porcelain")
	if err != nil {
		return RepoStatus{}, fmt.Errorf("读取仓库状态失败: %w", err)
	}
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 2 {
			continue
		}
		x, y := line[0], line[1]
		switch {
		case x == '?' && y == '?':
			status.Untracked++
		default:
			if x != ' ' {
				status.Staged++
			}
			if y != ' ' {
				status.Unstaged++
			}
		}
	}

	if url, err := runGit(workDir, gitCommandTimeout, "remote", "get-url", "origin"); err == nil {
		status.RemoteURL = strings.TrimSpace(url)
	}
	return status, nil
}

// ListBranches 列出本地分支，Current 标记当前分支。
func (s *Service) ListBranches(workDir string) ([]BranchInfo, error) {
	out, err := runGit(workDir, gitCommandTimeout, "branch", "--format=%(refname:short)%00%(HEAD)")
	if err != nil {
		return nil, fmt.Errorf("读取分支列表失败: %w", err)
	}
	branches := []BranchInfo{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 2)
		info := BranchInfo{Name: parts[0]}
		if len(parts) == 2 && parts[1] == "*" {
			info.Current = true
		}
		branches = append(branches, info)
	}
	return branches, nil
}

// validateBranchName 按 git refname 规则做保守校验：以 - 开头（会被当选项）、
// 含空白/非法字符、".."、"@{"、".lock" 结尾等一律拒绝，防止参数被误解析。
func validateBranchName(branch string) error {
	if strings.TrimSpace(branch) != branch || branch == "" {
		return errors.New("分支名不能为空或含首尾空白")
	}
	if strings.HasPrefix(branch, "-") {
		return errors.New("分支名不能以 - 开头")
	}
	if strings.ContainsAny(branch, " \t\n\r~^:?*[\\") {
		return errors.New("分支名包含非法字符（空白、~ ^ : ? * [ \\ 等）")
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		return errors.New("分支名包含非法序列（.. 或 @{）")
	}
	if strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".lock") || strings.HasSuffix(branch, ".") {
		return errors.New("分支名不能以 / 、. 或 .lock 结尾")
	}
	return nil
}

// SwitchBranch 切换本地分支。分支名先做非法字符校验再执行 git switch。
func (s *Service) SwitchBranch(workDir, branch string) error {
	if err := validateBranchName(branch); err != nil {
		return fmt.Errorf("分支名非法: %w", err)
	}
	if _, err := runGit(workDir, gitCommandTimeout, "switch", branch); err != nil {
		return fmt.Errorf("切换分支失败: %w", err)
	}
	return nil
}

// commitMessageSystemPrompt 是提交信息生成的 system prompt（中文），
// 风格对齐仓库既有提交样例（type(scope): 主题 + "- key: 变更点" 要点列表）。
const commitMessageSystemPrompt = `你是 Git 提交信息撰写助手。根据【最近提交风格】与【本次变更】输出一条提交信息：
- 首行 "type(scope): 主题"（type 如 feat/fix/docs/refactor/chore；scope 与主题用中文，主题不超过 50 字）
- 空行后以 "- key: 变更点" 形式列 3-6 条要点；key 从变更内容提炼（如 description/模块名/修复项），每个 key 尽量不重复
- 版本号变化写成 "模块: a.b.c -> d.e.f"
- 直接输出提交信息本身，禁止代码块包裹或任何解释`

// SummarizeDiff 调用设置页选定的终端预设（OpenAI 兼容 /chat/completions）
// 总结本次变更并生成提交信息，原样返回模型输出。untracked 文件只列文件名、
// 不读内容；完整 diff 截断到约 100KB 防止超大变更打爆上下文。
func (s *Service) SummarizeDiff(workDir string) (string, error) {
	// 1. 解析 settings 中的 preset 引用。
	ref := s.settings.GetCommitSummaryPreset()
	if ref == "" {
		return "", errors.New("请先在设置页选择提交总结模型")
	}
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("提交总结模型引用格式非法（%q），应为 provider/preset名，请在设置页重新选择", ref)
	}
	providerName, presetName := parts[0], parts[1]

	provider, ok := s.cfg.GetProviders()[providerName]
	if !ok {
		return "", fmt.Errorf("提交总结模型引用的 Provider %q 不存在，请在设置页重新选择", providerName)
	}
	if !strings.EqualFold(provider.EffectiveType(), "openai") {
		return "", errors.New("当前仅支持 OpenAI 兼容 Provider 的预设")
	}

	preset, ok := s.findTerminalPreset(providerName, presetName)
	if !ok {
		return "", fmt.Errorf("预设 %q 不存在（Provider %q），请在设置页重新选择", presetName, providerName)
	}

	model := preset.Model
	if model == "" {
		model = provider.DefaultModel
	}
	if model == "" {
		return "", fmt.Errorf("预设 %q 与 Provider %q 均未配置模型，无法生成提交信息", presetName, providerName)
	}

	// 运行时 HTTP 消费端按仓库约定使用归一化后的 base URL（EffectiveBaseURL，
	// 剥离 /chat/completions 后缀与尾斜杠），再统一拼接 /chat/completions，
	// 保证用户粘贴完整端点时不会出现双重后缀。
	baseURL := strings.TrimRight(provider.EffectiveBaseURL("openai"), "/")
	if baseURL == "" {
		return "", fmt.Errorf("Provider %q 未配置 BaseURL，无法生成提交信息", providerName)
	}

	var apiKey string
	if s.apiKeyResolver != nil {
		apiKey = strings.TrimSpace(s.apiKeyResolver(providerName))
	}
	if apiKey == "" {
		return "", fmt.Errorf("Provider %q 未配置 API Key，无法生成提交信息", providerName)
	}

	// 2. 收集变更材料（风格参考 + 文件清单 + diff）。
	var userPrompt strings.Builder
	userPrompt.WriteString("【最近提交风格】\n")
	if logOut, err := runGit(workDir, gitCommandTimeout, "log", "-8", "--pretty=format:%s"); err == nil && strings.TrimSpace(logOut) != "" {
		userPrompt.WriteString(logOut)
	} else {
		userPrompt.WriteString("（无历史提交）")
	}
	userPrompt.WriteString("\n\n【本次变更文件清单】\n")
	porcelain, err := runGit(workDir, gitCommandTimeout, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("读取仓库状态失败: %w", err)
	}
	untracked := []string{}
	if strings.TrimSpace(porcelain) == "" {
		userPrompt.WriteString("（无变更文件）")
	} else {
		for _, line := range strings.Split(porcelain, "\n") {
			if len(line) < 4 {
				continue
			}
			xy, file := line[:2], strings.TrimSpace(line[3:])
			userPrompt.WriteString(xy + " " + file + "\n")
			if strings.HasPrefix(xy, "??") {
				untracked = append(untracked, file)
			}
		}
	}
	if len(untracked) > 0 {
		userPrompt.WriteString("\n【未跟踪文件】（仅文件名，内容未知）\n")
		userPrompt.WriteString(strings.Join(untracked, "\n"))
	}

	userPrompt.WriteString("\n\n【变更统计】\n")
	if statOut, err := runGit(workDir, gitCommandTimeout, "diff", "HEAD", "--stat"); err == nil {
		userPrompt.WriteString(statOut)
	} else {
		userPrompt.WriteString("（暂无 diff）")
	}

	userPrompt.WriteString("\n\n【完整 diff】\n")
	if diffOut, err := runGit(workDir, gitCommandTimeout, "diff", "HEAD"); err == nil && diffOut != "" {
		userPrompt.WriteString(truncateBytes(diffOut, maxDiffBytes))
	} else {
		userPrompt.WriteString("（暂无 diff，仅未跟踪文件）")
	}

	// 3. 组装 OpenAI 兼容 chat/completions 请求，透传 preset 推理参数。
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": commitMessageSystemPrompt},
			{"role": "user", "content": userPrompt.String()},
		},
	}
	// 透传 preset parameters 里的推理参数（仅非零字段，与视觉导出契约口径一致）。
	p := preset.Parameters
	if p.ReasoningEffort != "" {
		body["reasoning_effort"] = p.ReasoningEffort
	}
	if p.MaxTokens != 0 {
		body["max_tokens"] = p.MaxTokens
	}
	if p.Temperature != 0 {
		body["temperature"] = p.Temperature
	}
	if p.TopP != 0 {
		body["top_p"] = p.TopP
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()
	url := baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求提交总结模型失败（%s）: %w", url, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("读取模型响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("提交总结模型返回错误（HTTP %d）: %s", resp.StatusCode, extractAPIErrorMessage(respBody))
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return "", fmt.Errorf("解析模型响应失败: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", errors.New("模型响应中没有 choices，无法生成提交信息")
	}
	return completion.Choices[0].Message.Content, nil
}

// findTerminalPreset 在两个公共桶（anthropic/openai）里按稳定 key（provider/preset）
// 或 Name+Provider 匹配查找终端预设。
func (s *Service) findTerminalPreset(providerName, presetName string) (config.TerminalPreset, bool) {
	for _, bucket := range []string{"anthropic", "openai"} {
		presets, err := s.cfg.GetTerminalPresets(bucket)
		if err != nil {
			continue
		}
		if tp, ok := presets[providerName+"/"+presetName]; ok {
			return tp, true
		}
		for _, tp := range presets {
			if tp.Provider == providerName && tp.Name == presetName {
				return tp, true
			}
		}
	}
	return config.TerminalPreset{}, false
}

// extractAPIErrorMessage 从 OpenAI 兼容错误响应体中提取 error.message；
// 提取不到时返回截断后的原始响应。
func extractAPIErrorMessage(body []byte) string {
	var errResp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return errResp.Error.Message
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 500 {
		msg = msg[:500] + "..."
	}
	return msg
}

// truncateBytes 按字节截断（遵守 maxBytes 上限），超出时追加截断标记。
func truncateBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// 回退到最后一个完整 UTF-8 序列边界，避免中文被切出乱码。
	cut := maxBytes
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncationMarker
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// commitViaStdin 用 git commit -F - 从 stdin 读取提交信息（多行/特殊字符
// 安全，不经命令行参数，无注入与转义问题）。
func commitViaStdin(workDir, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "commit", "-F", "-")
	platform.SuppressConsoleWindow(cmd)
	cmd.Stdin = strings.NewReader(message)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("提交失败: %s", detail)
	}
	return nil
}

// CommitAll 暂存全部变更（git add -A，含 untracked）后提交。
func (s *Service) CommitAll(workDir, message string) error {
	if strings.TrimSpace(message) == "" {
		return errors.New("提交信息不能为空")
	}
	if _, err := runGit(workDir, gitCommandTimeout, "add", "-A"); err != nil {
		return fmt.Errorf("暂存变更失败: %w", err)
	}
	return commitViaStdin(workDir, message)
}

// CommitStaged 仅提交已暂存变更（不执行 git add）。
func (s *Service) CommitStaged(workDir, message string) error {
	if strings.TrimSpace(message) == "" {
		return errors.New("提交信息不能为空")
	}
	return commitViaStdin(workDir, message)
}

// Push 推送当前分支。无上游时自动 git push --set-upstream origin <branch>。
// 返回值是 git push stderr 中的推送摘要（如 "main -> origin/main"）。
func (s *Service) Push(workDir string) (string, error) {
	push := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workDir, "push"}, args...)...)
		platform.SuppressConsoleWindow(cmd)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = err.Error()
			}
			return "", fmt.Errorf("推送失败: %s", detail)
		}
		return strings.TrimSpace(stderr.String()), nil
	}

	// 无上游分支时自动 --set-upstream origin <branch>。
	if _, err := runGit(workDir, gitCommandTimeout, "rev-parse", "--abbrev-ref", "@{upstream}"); err != nil {
		branch, berr := runGit(workDir, gitCommandTimeout, "branch", "--show-current")
		if berr != nil || strings.TrimSpace(branch) == "" {
			return "", errors.New("当前无上游分支且处于 detached HEAD，无法推送")
		}
		summary, err := push("--set-upstream", "origin", strings.TrimSpace(branch))
		if err != nil {
			return "", err
		}
		return joinPushSummary(summary), nil
	}
	summary, err := push()
	if err != nil {
		return "", err
	}
	return joinPushSummary(summary), nil
}

// joinPushSummary 把 stderr 摘要压成单行展示；空摘要回退固定成功文案。
func joinPushSummary(summary string) string {
	lines := []string{}
	for _, l := range strings.Split(summary, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "推送成功"
	}
	return strings.Join(lines, "；")
}
