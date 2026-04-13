package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	manifestVersion  = "1"
	manifestFileName = ".amagi-codebox-manifest.json"
)

func defaultManifest() DeploymentManifest {
	return DeploymentManifest{Version: manifestVersion, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Entries: []DeploymentEntry{}}
}

func ManifestPathForWorkspace(workspacePath string) string {
	return filepath.Join(workspacePath, manifestFileName)
}

func ReadManifest(path string) (DeploymentManifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultManifest(), nil
		}
		return DeploymentManifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest DeploymentManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return DeploymentManifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.Version == "" {
		manifest.Version = manifestVersion
	}
	if manifest.Entries == nil {
		manifest.Entries = []DeploymentEntry{}
	}
	sortManifestEntries(manifest.Entries)
	return manifest, nil
}

func WriteManifest(path string, manifest DeploymentManifest) error {
	manifest.Version = manifestVersion
	manifest.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if manifest.Entries == nil {
		manifest.Entries = []DeploymentEntry{}
	}
	sortManifestEntries(manifest.Entries)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir manifest dir: %w", err)
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace manifest: %w", err)
	}
	return nil
}

type ManifestDiff struct {
	ToRemove []DeploymentEntry
	ToUpsert []DeploymentEntry
}

func DiffManifest(current, desired DeploymentManifest) ManifestDiff {
	currentMap := make(map[string]DeploymentEntry, len(current.Entries))
	for _, entry := range current.Entries {
		currentMap[deploymentEntryKey(entry)] = entry
	}
	desiredMap := make(map[string]DeploymentEntry, len(desired.Entries))
	for _, entry := range desired.Entries {
		desiredMap[deploymentEntryKey(entry)] = entry
	}
	var diff ManifestDiff
	for key, entry := range currentMap {
		if _, ok := desiredMap[key]; !ok {
			diff.ToRemove = append(diff.ToRemove, entry)
		}
	}
	for key, entry := range desiredMap {
		currentEntry, ok := currentMap[key]
		if !ok || currentEntry.Checksum != entry.Checksum || currentEntry.PluginVersion != entry.PluginVersion || currentEntry.Status != entry.Status {
			diff.ToUpsert = append(diff.ToUpsert, entry)
		}
	}
	sortManifestEntries(diff.ToRemove)
	sortManifestEntries(diff.ToUpsert)
	return diff
}

func deploymentEntryKey(entry DeploymentEntry) string {
	return strings.Join([]string{entry.PluginID, string(entry.SubItemRef.Type), entry.SubItemRef.Name, filepath.ToSlash(entry.TargetPath), string(entry.SourceScope), entry.ContentMarker}, "|")
}

func sortManifestEntries(entries []DeploymentEntry) {
	sort.Slice(entries, func(i, j int) bool {
		ki := deploymentEntryKey(entries[i])
		kj := deploymentEntryKey(entries[j])
		return ki < kj
	})
}
