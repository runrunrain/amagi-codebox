package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

func checkPlanConflicts(root string, current DeploymentManifest, plan deploymentPlan) []Conflict {
	currentTargets := groupEntriesByTarget(current.Entries)
	conflicts := []Conflict{}
	seenExclusive := map[string]DeploymentEntry{}
	for _, file := range plan.Files {
		if existing, ok := seenExclusive[file.Entry.TargetPath]; ok && existing.PluginID != file.Entry.PluginID {
			conflicts = append(conflicts, Conflict{Type: ConflictTypeTargetPath, PluginID: file.Entry.PluginID, TargetPath: file.Entry.TargetPath, Message: fmt.Sprintf("目标路径 %s 被多个插件同时占用", file.Entry.TargetPath), Blocking: true})
			continue
		}
		seenExclusive[file.Entry.TargetPath] = file.Entry
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file.Entry.TargetPath))); err == nil {
			if _, ok := currentTargets[file.Entry.TargetPath]; !ok {
				conflicts = append(conflicts, Conflict{Type: ConflictTypeUserFile, PluginID: file.Entry.PluginID, TargetPath: file.Entry.TargetPath, Message: fmt.Sprintf("目标路径 %s 已存在非托管文件", file.Entry.TargetPath), Blocking: true})
			}
		}
	}
	seenMergedMarkers := map[string]DeploymentEntry{}
	for _, merged := range plan.Merged {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(merged.TargetPath))); err == nil {
			if _, ok := currentTargets[merged.TargetPath]; !ok {
				conflicts = append(conflicts, Conflict{Type: ConflictTypeUserFile, TargetPath: merged.TargetPath, Message: fmt.Sprintf("目标路径 %s 已存在非托管文件", merged.TargetPath), Blocking: true})
			}
		}
		for _, entry := range merged.Entries {
			key := merged.TargetPath + "|" + entry.ContentMarker
			if existing, ok := seenMergedMarkers[key]; ok && existing.PluginID != entry.PluginID {
				conflicts = append(conflicts, Conflict{Type: ConflictTypeMCPKey, PluginID: entry.PluginID, TargetPath: merged.TargetPath, Message: fmt.Sprintf("共享文件 %s 中存在重复片段标识 %s", merged.TargetPath, entry.ContentMarker), Blocking: true})
				continue
			}
			seenMergedMarkers[key] = entry
		}
	}
	return conflicts
}

func groupEntriesByTarget(entries []DeploymentEntry) map[string][]DeploymentEntry {
	out := make(map[string][]DeploymentEntry)
	for _, entry := range entries {
		out[entry.TargetPath] = append(out[entry.TargetPath], entry)
	}
	return out
}
