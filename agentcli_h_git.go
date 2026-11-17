package main

import (
	"context"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/gitassist"
)

// 本文件实现 git 域 Handler（repo-info / branches）。只读面：commit/push/switch
// 按设计 §3.5 明确排除在 CLI 之外。gitassist 查询接口无 ctx 参数：Handler
// 入口检查取消（D-06 的 CLI 侧取消边界），命令 Notes 已写明可安全重试。

func agentCLIGitWorkDir(args spec.Args) (string, *spec.Error) {
	dir := strings.TrimSpace(args.String("dir"))
	if dir == "" {
		return "", agentCLIArgErrf("缺少必填参数 dir")
	}
	return dir, nil
}

func (h *agentCLIHost) gitService() (*gitassist.Service, *spec.Error) {
	if h.app == nil || h.app.GitAssist == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "gitassist 服务未初始化")
	}
	return h.app.GitAssist, nil
}

type agentCLIRepoInfoView struct {
	WorkDir string `json:"workDir"`
	gitassist.RepoStatus
}

func (h *agentCLIHost) handleGitRepoInfo(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.gitService()
	if serr != nil {
		return nil, serr
	}
	dir, aerr := agentCLIGitWorkDir(args)
	if aerr != nil {
		return nil, aerr
	}
	status, err := svc.RepoInfo(dir)
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return agentCLIRepoInfoView{WorkDir: dir, RepoStatus: status}, nil
}

func (h *agentCLIHost) handleGitBranches(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.gitService()
	if serr != nil {
		return nil, serr
	}
	dir, aerr := agentCLIGitWorkDir(args)
	if aerr != nil {
		return nil, aerr
	}
	branches, err := svc.ListBranches(dir)
	if err != nil {
		// 非 git 仓库时 git branch 报错，映射为 not_found 并保留原始语义。
		return nil, agentCLIServiceErr(err)
	}
	if branches == nil {
		branches = []gitassist.BranchInfo{}
	}
	return map[string]any{"workDir": dir, "branches": branches, "count": len(branches)}, nil
}

func (h *agentCLIHost) gitHandlers() map[string]spec.Handler {
	return map[string]spec.Handler{
		"git.repo-info": h.handleGitRepoInfo,
		"git.branches":  h.handleGitBranches,
	}
}
