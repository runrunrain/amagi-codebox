package opencodeplugin

import (
	"amagi-codebox/internal/platform"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const npmRegistryURL = "https://registry.npmjs.org"

var (
	stableVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type stableVersion struct {
	Major int
	Minor int
	Patch int
}

type updateTarget struct {
	Spec    string
	Version string
}

type githubRepository struct {
	Owner string
	Name  string
	Ref   string
}

func parseStableVersion(raw string) (stableVersion, bool) {
	match := stableVersionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return stableVersion{}, false
	}
	major, errMajor := strconv.Atoi(match[1])
	minor, errMinor := strconv.Atoi(match[2])
	patch, errPatch := strconv.Atoi(match[3])
	if errMajor != nil || errMinor != nil || errPatch != nil {
		return stableVersion{}, false
	}
	return stableVersion{Major: major, Minor: minor, Patch: patch}, true
}

func (v stableVersion) Compare(other stableVersion) int {
	if v.Major != other.Major {
		if v.Major > other.Major {
			return 1
		}
		return -1
	}
	if v.Minor != other.Minor {
		if v.Minor > other.Minor {
			return 1
		}
		return -1
	}
	if v.Patch > other.Patch {
		return 1
	}
	if v.Patch < other.Patch {
		return -1
	}
	return 0
}

func (v stableVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (s *Service) resolveUpdateTarget(spec string) (updateTarget, error) {
	switch pluginSource(spec) {
	case "github":
		repository, err := parseGitHubRepository(spec)
		if err != nil {
			return updateTarget{}, err
		}
		tag, version, err := s.latestGitHubTag(repository)
		if err != nil {
			return updateTarget{}, err
		}
		return updateTarget{
			Spec:    fmt.Sprintf("github:%s/%s#%s", repository.Owner, repository.Name, tag),
			Version: version.String(),
		}, nil
	case "npm":
		name, err := npmPackageName(spec)
		if err != nil {
			return updateTarget{}, err
		}
		version, err := s.latestNPMVersion(name)
		if err != nil {
			return updateTarget{}, err
		}
		return updateTarget{
			Spec:    fmt.Sprintf("%s@%s", name, version.String()),
			Version: version.String(),
		}, nil
	default:
		return updateTarget{}, fmt.Errorf("本地 file 插件直接从原路径加载，无需执行远端版本更新: %s", spec)
	}
}

func parseGitHubRepository(spec string) (githubRepository, error) {
	raw := strings.TrimSpace(spec)
	if strings.HasPrefix(raw, "github:") {
		return githubRepositoryFromPath(strings.TrimPrefix(raw, "github:"))
	}
	if strings.HasPrefix(raw, "git@github.com:") {
		return githubRepositoryFromPath(strings.TrimPrefix(raw, "git@github.com:"))
	}

	parsedRaw := strings.TrimPrefix(raw, "git+")
	parsed, err := url.Parse(parsedRaw)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return githubRepository{}, fmt.Errorf("不支持的 GitHub 插件地址: %s", spec)
	}
	repository, err := githubRepositoryFromPath(strings.TrimPrefix(parsed.Path, "/") + fragmentSuffix(parsed.Fragment))
	if err != nil {
		return githubRepository{}, err
	}
	return repository, nil
}

func fragmentSuffix(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}
	return "#" + fragment
}

func githubRepositoryFromPath(raw string) (githubRepository, error) {
	repositoryPath, ref, _ := strings.Cut(strings.TrimSpace(raw), "#")
	repositoryPath = strings.TrimSuffix(strings.Trim(repositoryPath, "/"), ".git")
	segments := strings.Split(repositoryPath, "/")
	if len(segments) != 2 || strings.TrimSpace(segments[0]) == "" || strings.TrimSpace(segments[1]) == "" {
		return githubRepository{}, fmt.Errorf("GitHub 插件地址必须为 owner/repo: %s", raw)
	}
	return githubRepository{
		Owner: segments[0],
		Name:  segments[1],
		Ref:   strings.TrimSpace(ref),
	}, nil
}

func (s *Service) latestGitHubTag(repository githubRepository) (string, stableVersion, error) {
	remote := fmt.Sprintf("https://github.com/%s/%s.git", repository.Owner, repository.Name)
	output, err := s.runResolvedCommand("git", 45*time.Second, "ls-remote", "--tags", "--refs", remote)
	if err != nil {
		return "", stableVersion{}, fmt.Errorf("查询 GitHub 插件版本失败: %w", err)
	}

	var latest stableVersion
	latestTag := ""
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		tag := strings.TrimPrefix(fields[1], "refs/tags/")
		version, ok := parseStableVersion(tag)
		if !ok {
			continue
		}
		if latestTag == "" || version.Compare(latest) > 0 || (version.Compare(latest) == 0 && strings.HasPrefix(tag, "v")) {
			latest = version
			latestTag = tag
		}
	}
	if latestTag == "" {
		return "", stableVersion{}, fmt.Errorf("GitHub 仓库 %s/%s 没有可用的稳定 SemVer tag", repository.Owner, repository.Name)
	}
	return latestTag, latest, nil
}

func npmPackageName(spec string) (string, error) {
	raw := strings.TrimSpace(spec)
	if raw == "" || strings.ContainsAny(raw, "/\\") && !strings.HasPrefix(raw, "@") {
		return "", fmt.Errorf("不支持的 npm 插件地址: %s", spec)
	}
	if strings.HasPrefix(raw, "@") {
		slash := strings.Index(raw, "/")
		if slash <= 1 {
			return "", fmt.Errorf("无效的 scoped npm 插件名: %s", spec)
		}
		if versionAt := strings.LastIndex(raw, "@"); versionAt > slash {
			raw = raw[:versionAt]
		}
		if strings.Count(raw, "/") != 1 {
			return "", fmt.Errorf("无效的 scoped npm 插件名: %s", spec)
		}
		return raw, nil
	}
	if versionAt := strings.LastIndex(raw, "@"); versionAt > 0 {
		raw = raw[:versionAt]
	}
	if raw == "" || strings.ContainsAny(raw, ":#") {
		return "", fmt.Errorf("无效的 npm 插件名: %s", spec)
	}
	return raw, nil
}

func (s *Service) latestNPMVersion(name string) (stableVersion, error) {
	requestURL := npmRegistryURL + "/" + url.PathEscape(name) + "/latest"
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return stableVersion{}, fmt.Errorf("创建 npm 版本请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "amagi-codebox-opencode-plugin")

	client := s.http
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return stableVersion{}, fmt.Errorf("请求 npm latest 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return stableVersion{}, fmt.Errorf("npm latest 返回 %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var document struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		return stableVersion{}, fmt.Errorf("解析 npm latest 失败: %w", err)
	}
	version, ok := parseStableVersion(document.Version)
	if !ok {
		return stableVersion{}, fmt.Errorf("npm latest 不是稳定 SemVer: %s", document.Version)
	}
	return version, nil
}

func (s *Service) runResolvedCommand(command string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resolver := s.resolver
	if resolver == nil {
		resolver = platform.NewCLIResolver(platform.CurrentCapabilities())
	}
	env := platform.BuildEffectiveEnv(os.Environ())
	cli, _, err := resolver.ResolveExecutable(command, append([]string(nil), args...), env)
	if err != nil {
		return "", fmt.Errorf("未找到 %s CLI: %w", command, err)
	}
	runner := s.runner
	if runner == nil {
		runner = platform.NewProcessRunner()
	}
	result, err := runner.Run(ctx, platform.CommandSpec{
		Path:   cli.Path,
		Args:   cli.Args,
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s 命令执行超时", command)
	}
	if err != nil {
		detail := ""
		if result != nil {
			detail = strings.TrimSpace(result.Stderr)
		}
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("%s 命令执行失败: %s", command, detail)
	}
	if result == nil {
		return "", fmt.Errorf("%s 命令没有返回结果", command)
	}
	return strings.TrimSpace(result.Stdout), nil
}

func alreadyCurrentResult(spec string, current PluginDetail, target updateTarget) *CommandResult {
	currentVersion, currentVersionOK := parseStableVersion(current.Version)
	targetVersion, _ := parseStableVersion(target.Version)
	if currentVersionOK && currentVersion.Compare(targetVersion) > 0 {
		return &CommandResult{
			Success: true,
			Output:  fmt.Sprintf("%s 的本地版本 %s 高于远端稳定版本 %s，未执行降级", spec, current.Version, target.Version),
		}
	}
	if spec != target.Spec {
		return nil
	}
	if current.InstallPath == "" {
		return nil
	}
	if !currentVersionOK || currentVersion.Compare(targetVersion) != 0 {
		return nil
	}
	return &CommandResult{
		Success: true,
		Output:  fmt.Sprintf("%s 已是最新不可变版本 %s", spec, target.Version),
	}
}

func (s *Service) verifyUpdatedPlugin(previousSpec string, target updateTarget) error {
	configured, err := s.readConfiguredSpecs()
	if err != nil {
		return fmt.Errorf("更新命令完成，但读取 OpenCode 配置失败: %w", err)
	}
	found := false
	for _, spec := range configured {
		if spec == target.Spec {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("更新命令完成，但 OpenCode 配置未切换到 %s", target.Spec)
	}

	current := s.inspectPlugin(target.Spec, false)
	if current.InstallPath == "" {
		return fmt.Errorf("更新命令完成，但没有找到 %s 的缓存安装包", target.Spec)
	}
	if current.Version != target.Version {
		return fmt.Errorf("更新命令完成，但缓存包版本为 %s，期望 %s", current.Version, target.Version)
	}
	if previousSpec != target.Spec {
		for _, spec := range configured {
			if spec == previousSpec {
				return fmt.Errorf("更新命令完成，但旧插件引用仍存在: %s", previousSpec)
			}
		}
	}
	return nil
}
