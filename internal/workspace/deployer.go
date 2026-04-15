package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Service) applyPlan(targetID, root, manifestPath string, plan deploymentPlan, warnings []string) (DeployResult, error) {
	normalizedPlan, err := normalizeDeploymentPlan(plan)
	if err != nil {
		return DeployResult{}, err
	}
	current, err := ReadManifest(manifestPath)
	if err != nil {
		return DeployResult{}, err
	}
	activeEntries, orphanedEntries := splitManifestEntriesByStatus(current.Entries)
	activeManifest := defaultManifest()
	activeManifest.Entries = activeEntries

	result := DeployResult{TargetID: targetID, Warnings: append([]string{}, warnings...), Manifest: current, Deployed: []DeploymentEntry{}, Removed: []string{}, Conflicts: []Conflict{}}
	conflicts := checkPlanConflicts(root, activeManifest, normalizedPlan)
	currentTargets := groupEntriesByTarget(activeEntries)
	desiredTargets := map[string]struct{}{}
	for _, file := range normalizedPlan.Files {
		desiredTargets[file.Entry.TargetPath] = struct{}{}
	}
	for _, merged := range normalizedPlan.Merged {
		desiredTargets[merged.TargetPath] = struct{}{}
	}
	for target, entries := range currentTargets {
		if len(entries) == 0 {
			continue
		}
		changed, err := managedTargetModified(root, target, entries)
		if err != nil {
			return DeployResult{}, err
		}
		if changed {
			conflicts = append(conflicts, Conflict{Type: ConflictTypeModifiedFile, PluginID: entries[0].PluginID, TargetPath: target, Message: fmt.Sprintf("托管文件 %s 已被手动修改，当前操作不会覆盖", target), Blocking: true})
		}
	}
	if len(conflicts) > 0 {
		result.Conflicts = conflicts
		return result, fmt.Errorf("deployment conflicts detected")
	}
	for target := range currentTargets {
		if _, ok := desiredTargets[target]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(target))); err != nil && !os.IsNotExist(err) {
			return DeployResult{}, fmt.Errorf("remove stale target %s: %w", target, err)
		}
		result.Removed = append(result.Removed, target)
	}

	entriesByTarget := map[string][]DeploymentEntry{}
	for _, file := range normalizedPlan.Files {
		absPath := filepath.Join(root, filepath.FromSlash(file.Entry.TargetPath))
		content, err := plannedFileContent(file)
		if err != nil {
			return DeployResult{}, err
		}
		if err := writeFile(absPath, content); err != nil {
			return DeployResult{}, err
		}
		entry := file.Entry
		entry.Checksum = sha256Hex(content)
		entriesByTarget[entry.TargetPath] = append(entriesByTarget[entry.TargetPath], entry)
		result.Deployed = append(result.Deployed, entry)
	}
	for _, merged := range normalizedPlan.Merged {
		absPath := filepath.Join(root, filepath.FromSlash(merged.TargetPath))
		if err := writeFile(absPath, merged.Content); err != nil {
			return DeployResult{}, err
		}
		checksum := sha256Hex(merged.Content)
		for _, raw := range merged.Entries {
			entry := raw
			entry.Checksum = checksum
			entriesByTarget[entry.TargetPath] = append(entriesByTarget[entry.TargetPath], entry)
			result.Deployed = append(result.Deployed, entry)
		}
	}

	manifest := defaultManifest()
	for _, entries := range entriesByTarget {
		manifest.Entries = append(manifest.Entries, entries...)
	}
	manifest.Entries = append(manifest.Entries, filterUnownedOrphanedEntries(orphanedEntries, manifest.Entries)...)
	if err := WriteManifest(manifestPath, manifest); err != nil {
		return DeployResult{}, err
	}
	result.Manifest = manifest
	return result, nil
}

func (s *Service) cleanManifestTargets(targetID, root, manifestPath string) (CleanResult, error) {
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return CleanResult{}, err
	}
	result := CleanResult{TargetID: targetID, Warnings: []string{}, Manifest: manifest, Removed: []string{}}
	remaining := make([]DeploymentEntry, 0, len(manifest.Entries))
	for target, entries := range groupEntriesByTarget(manifest.Entries) {
		changed, err := managedTargetModified(root, target, entries)
		if err != nil {
			return CleanResult{}, err
		}
		if changed {
			result.Warnings = append(result.Warnings, fmt.Sprintf("托管文件 %s 已被手动修改，已跳过清理", target))
			remaining = append(remaining, entries...)
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(target))); err != nil && !os.IsNotExist(err) {
			return CleanResult{}, fmt.Errorf("remove target %s: %w", target, err)
		}
		result.Removed = append(result.Removed, target)
	}
	manifest = defaultManifest()
	manifest.Entries = remaining
	if err := WriteManifest(manifestPath, manifest); err != nil {
		return CleanResult{}, err
	}
	result.Manifest = manifest
	return result, nil
}
func splitManifestEntriesByStatus(entries []DeploymentEntry) ([]DeploymentEntry, []DeploymentEntry) {
	active := make([]DeploymentEntry, 0, len(entries))
	orphaned := make([]DeploymentEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Status == DeploymentStatusOrphaned {
			orphaned = append(orphaned, entry)
			continue
		}
		active = append(active, entry)
	}
	return active, orphaned
}

func filterUnownedOrphanedEntries(orphaned, desired []DeploymentEntry) []DeploymentEntry {
	owned := make(map[string]struct{}, len(desired))
	for _, entry := range desired {
		owned[deploymentEntryKey(entry)] = struct{}{}
	}
	filtered := make([]DeploymentEntry, 0, len(orphaned))
	for _, entry := range orphaned {
		if _, ok := owned[deploymentEntryKey(entry)]; ok {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func managedTargetModified(root, target string, entries []DeploymentEntry) (bool, error) {
	if len(entries) == 0 {
		return false, nil
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return sha256Hex(content) != entries[0].Checksum, nil
}

func plannedFileContent(file plannedFile) ([]byte, error) {
	if file.Content != nil {
		return file.Content, nil
	}
	content, err := os.ReadFile(file.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("read source %s: %w", file.SourcePath, err)
	}
	return content, nil
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func normalizeDeploymentPlan(plan deploymentPlan) (deploymentPlan, error) {
	normalized := deploymentPlan{Files: append([]plannedFile{}, plan.Files...), Merged: []plannedMergedFile{}}
	grouped := make(map[string][]plannedMergedFile)
	order := make([]string, 0)
	for _, merged := range plan.Merged {
		if _, ok := grouped[merged.TargetPath]; !ok {
			order = append(order, merged.TargetPath)
		}
		grouped[merged.TargetPath] = append(grouped[merged.TargetPath], merged)
	}
	for _, target := range order {
		merged, err := mergePlannedMergedFiles(target, grouped[target])
		if err != nil {
			return deploymentPlan{}, err
		}
		normalized.Merged = append(normalized.Merged, merged)
	}
	return normalized, nil
}

func mergePlannedMergedFiles(target string, items []plannedMergedFile) (plannedMergedFile, error) {
	merged := plannedMergedFile{TargetPath: target, Entries: []DeploymentEntry{}}
	for _, item := range items {
		merged.Entries = append(merged.Entries, item.Entries...)
	}
	if filepath.Ext(target) == ".json" {
		content, err := mergeJSONContent(items)
		if err != nil {
			return plannedMergedFile{}, err
		}
		merged.Content = content
		return merged, nil
	}
	merged.Content = mergeTextContent(items)
	return merged, nil
}

func mergeJSONContent(items []plannedMergedFile) ([]byte, error) {
	acc := map[string]interface{}{}
	for _, item := range items {
		var payload map[string]interface{}
		if err := json.Unmarshal(item.Content, &payload); err != nil {
			return nil, fmt.Errorf("parse merged json %s: %w", item.TargetPath, err)
		}
		acc = mergeJSONObject(acc, payload)
	}
	b, err := json.MarshalIndent(acc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal merged json: %w", err)
	}
	return append(b, '\n'), nil
}

func mergeJSONObject(left, right map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range left {
		out[k] = v
	}
	for k, rv := range right {
		lv, ok := out[k]
		if !ok {
			out[k] = rv
			continue
		}
		out[k] = mergeJSONValue(lv, rv)
	}
	return out
}

func mergeJSONValue(left, right interface{}) interface{} {
	leftMap, leftIsMap := left.(map[string]interface{})
	rightMap, rightIsMap := right.(map[string]interface{})
	if leftIsMap && rightIsMap {
		return mergeJSONObject(leftMap, rightMap)
	}
	leftSlice, leftIsSlice := left.([]interface{})
	rightSlice, rightIsSlice := right.([]interface{})
	if leftIsSlice && rightIsSlice {
		return append(leftSlice, rightSlice...)
	}
	return right
}

func mergeTextContent(items []plannedMergedFile) []byte {
	parts := make([][]byte, 0, len(items))
	for _, item := range items {
		if len(item.Content) == 0 {
			continue
		}
		parts = append(parts, item.Content)
	}
	if len(parts) == 0 {
		return []byte{}
	}
	joined := bytes.Join(parts, []byte("\n"))
	if len(joined) == 0 || joined[len(joined)-1] != '\n' {
		joined = append(joined, '\n')
	}
	return joined
}
