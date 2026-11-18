package main

// app_sharedmcp_import.go — 共用 MCP「粘贴 JSON / 选择 JSON 文件」导入的
// App 门面。机制本体在
// internal/sharedmcp/import.go；本文件只负责：
//   - ParseSharedMcpImportText：粘贴文本的纯解析预览（只含名称/类型/状态，
//     不回传任何配置值）；
//   - PickSharedMcpImportFile：预览所选文件；原文留在后端，前端仅获得确认凭据。
//   - ImportSharedMcpEntries：显式提交——后端重新解析（以提交时登记现状
//     裁定同名冲突）批量登记，默认无分发，返回逐条导入/跳过报告 + 最新
//     Overview。
//
// 不自动重分发：导入条目与手工 Create 同语义，分发仍由用户在矩阵显式勾选。

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"amagi-codebox/internal/sharedmcp"
)

// SharedMcpImportPreview 是纯解析预览的平铺门面视图（平铺原因同
// SharedMcpOverview：wails 绑定生成器不递归内嵌结构体）。
type SharedMcpImportPreview struct {
	Entries []sharedmcp.ImportPreviewEntry `json:"entries"`
}

// SharedMcpImportFilePreview excludes the selected file's raw contents.
type SharedMcpImportFilePreview struct {
	Entries []sharedmcp.ImportPreviewEntry `json:"entries"`
	// FileName 是所选文件名（UI 展示来源用）。
	FileName string `json:"fileName"`
	Ticket   string `json:"ticket"`
}

// Only the most recently previewed file can be confirmed. It is held in memory,
// can expire, and is consumed once so preview cannot be silently replayed.
type sharedMcpFileDraft struct {
	mu           sync.Mutex
	ticket, text string
	expires      time.Time
}

// SharedMcpImportOutcome 是显式提交的结果报告 + 最新 Overview。
type SharedMcpImportOutcome struct {
	Imported []string                  `json:"imported"`
	Skipped  []sharedmcp.ImportSkipped `json:"skipped"`
	Overview SharedMcpOverview         `json:"overview"`
}

// ParseSharedMcpImportText 解析粘贴的 JSON 并返回预览（不落盘）：只含
// 名称/类型/状态/附注，不含任何配置值；与现行登记比对标注同名冲突。
func (a *App) ParseSharedMcpImportText(text string) (*SharedMcpImportPreview, error) {
	pv, err := a.sharedMcpService().BuildImportPreview(text)
	if err != nil {
		return nil, err
	}
	return &SharedMcpImportPreview{Entries: pv.Entries}, nil
}

// PickSharedMcpImportFile 弹出系统文件选择对话框（*.json），读取所选文件
// 并解析预览。读取仅针对对话框返回的路径，且读取前 Stat 校验大小不超过
// sharedmcp.MaxImportBytes；用户取消 → (nil, nil)。选文件即预览，提交仍需
// 用户显式点击「导入」（ImportPickedSharedMcpFile）。
func (a *App) PickSharedMcpImportFile() (*SharedMcpImportFilePreview, error) {
	filePath, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择要导入的 MCP JSON 文件",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件选择对话框失败: %w", err)
	}
	if filePath == "" {
		return nil, nil // 用户取消
	}
	fi, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取所选文件失败: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("所选路径不是普通文件")
	}
	if fi.Size() > int64(sharedmcp.MaxImportBytes) {
		return nil, fmt.Errorf("所选文件超过导入大小上限 %d KB: %s（约 %d KB）",
			sharedmcp.MaxImportBytes/1024, filePath, (fi.Size()+1023)/1024)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取所选文件失败: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, sharedmcp.MaxImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取所选文件失败: %w", err)
	}
	if len(data) > sharedmcp.MaxImportBytes {
		return nil, fmt.Errorf("所选文件超过导入大小上限 %d KB", sharedmcp.MaxImportBytes/1024)
	}
	out, err := a.parseSharedMcpImportFileData(data, filePath)
	if err != nil {
		return nil, err
	}
	if a.Log != nil {
		a.Log.Info("sharedmcp", "导入文件解析预览", fmt.Sprintf("file=%s entries=%d", filePath, len(out.Entries)))
	}
	return out, nil
}

// parseSharedMcpImportFileData strips a BOM and stages the previewed bytes for confirmation.
func (a *App) parseSharedMcpImportFileData(data []byte, displayPath string) (*SharedMcpImportFilePreview, error) {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}
	pv, err := a.sharedMcpService().BuildImportPreview(string(data))
	if err != nil {
		return nil, err
	}
	ticket := rand.Text()
	draft := &a.sharedMcpImport
	draft.mu.Lock()
	draft.ticket, draft.text, draft.expires = ticket, string(data), time.Now().Add(10*time.Minute)
	draft.mu.Unlock()
	return &SharedMcpImportFilePreview{Entries: pv.Entries, FileName: filepath.Base(displayPath), Ticket: ticket}, nil
}

func (a *App) ImportPickedSharedMcpFile(ticket string) (*SharedMcpImportOutcome, error) {
	draft := &a.sharedMcpImport
	draft.mu.Lock()
	if ticket == "" || ticket != draft.ticket || time.Now().After(draft.expires) {
		draft.mu.Unlock()
		return nil, fmt.Errorf("文件预览已失效，请重新选择 JSON 文件")
	}
	text := draft.text
	draft.ticket, draft.text = "", ""
	draft.mu.Unlock()
	return a.ImportSharedMcpEntries(text)
}

// ImportSharedMcpEntries 显式提交导入：后端重新解析文本（以提交时登记现状
// 裁定同名冲突）并批量登记（同名跳过逐条报告、未知配置值零丢弃原样写入、
// 默认无分发），返回导入/跳过报告 + 最新 Overview。
func (a *App) ImportSharedMcpEntries(text string) (*SharedMcpImportOutcome, error) {
	outcome, err := a.sharedMcpService().Import(text)
	if err != nil {
		return nil, err
	}
	ov, err := a.sharedMcpOverview()
	if err != nil {
		return nil, err
	}
	return &SharedMcpImportOutcome{
		Imported: outcome.Imported,
		Skipped:  outcome.Skipped,
		Overview: *ov,
	}, nil
}
