package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Service) applyPlan(targetID, root, manifestPath string, plan deploymentPlan, warnings []string) (DeployResult, error) {
	current, err := ReadManifest(manifestPath)
	if err != nil {
		return DeployResult{}, err
	}
	activeEntries, orphanedEntries := splitManifestEntriesByStatus(current.Entries)
	activeManifest := defaultManifest()
	activeManifest.Entries = activeEntries

	result := DeployResult{TargetID: targetID, Warnings: append([]string{}, warnings...), Manifest: current, Deployed: []DeploymentEntry{}, Removed: []string{}, Conflicts: []Conflict{}}
	conflicts := checkPlanConflicts(root, activeManifest, plan)
	currentTargets := groupEntriesByTarget(activeEntries)
	desiredTargets := map[string]struct{}{}
	for _, file := range plan.Files {
		desiredTargets[file.Entry.TargetPath] = struct{}{}
	}
	for _, merged := range plan.Merged {
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
	for _, file := range plan.Files {
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
	for _, merged := range plan.Merged {
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
