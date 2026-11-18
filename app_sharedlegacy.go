package main

import (
	"fmt"
	"path/filepath"

	"amagi-codebox/internal/sharedext"
)

// LegacySharedExtensionsSummary reports only counts and paths, never MCP definitions.
// The retired v1.3.145 depot is not included in the current listings or written here.
type LegacySharedExtensionsSummary struct {
	Path   string `json:"path"`
	Skills int    `json:"skills"`
	MCP    int    `json:"mcp"`
}

func (a *App) GetLegacySharedExtensionsSummary() (LegacySharedExtensionsSummary, error) {
	if a.configDir == "" {
		return LegacySharedExtensionsSummary{}, nil
	}
	skills, mcp, err := sharedext.NewService(a.configDir, nil).RegistryCounts()
	if err != nil {
		return LegacySharedExtensionsSummary{}, fmt.Errorf("无法读取旧版共用扩展登记（未修改文件）")
	}
	return LegacySharedExtensionsSummary{
		Path:   filepath.Join(a.configDir, "shared"),
		Skills: skills,
		MCP:    mcp,
	}, nil
}
