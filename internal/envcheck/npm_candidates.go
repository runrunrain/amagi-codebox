package envcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// npmGlobalCommandCandidates returns the executable locations npm may expose
// for a globally installed package. npm prefix and npm root are deliberately
// both inputs: on Unix the package root is normally <prefix>/lib/node_modules,
// not <prefix>/node_modules.
func npmGlobalCommandCandidates(prefix, npmRoot, command, packageName string) []string {
	prefix = filepath.Clean(strings.TrimSpace(prefix))
	if prefix == "" || prefix == "." {
		return nil
	}

	npmRoot = filepath.Clean(strings.TrimSpace(npmRoot))
	if npmRoot == "" || npmRoot == "." {
		npmRoot = inferNPMNodeModulesFromPrefix(prefix)
	}

	dirs := []string{
		filepath.Join(prefix, "bin"),
		prefix,
		filepath.Join(npmRoot, ".bin"),
	}
	names := npmGlobalCommandNames(command)
	candidates := make([]string, 0, len(dirs)*len(names)+3)
	seen := map[string]struct{}{}
	appendCandidate := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			return
		}
		key := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, path)
	}

	for _, dir := range dirs {
		for _, name := range names {
			appendCandidate(filepath.Join(dir, name))
		}
	}
	for _, candidate := range npmPackageBinCandidates(npmRoot, packageName, command) {
		appendCandidate(candidate)
	}
	return candidates
}

func npmGlobalCommandNames(command string) []string {
	if isWindows() {
		return []string{command + ".cmd", command + ".exe", command}
	}
	return []string{command}
}

// npmPackageBinCandidates reads the installed package's own bin declaration.
// It is intentionally a final fallback for a missing npm shim: executing the
// package entry through the enhanced environment is still safer than treating
// a successfully installed package as absent.
func npmPackageBinCandidates(npmRoot, packageName, command string) []string {
	npmRoot = filepath.Clean(strings.TrimSpace(npmRoot))
	if npmRoot == "" || npmRoot == "." || packageName == "" || command == "" {
		return nil
	}
	packageRoot := filepath.Join(npmRoot, filepath.FromSlash(packageName))
	data, err := os.ReadFile(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return nil
	}

	var manifest struct {
		Bin json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || len(manifest.Bin) == 0 {
		return nil
	}

	var single string
	if err := json.Unmarshal(manifest.Bin, &single); err == nil && strings.TrimSpace(single) != "" {
		return []string{filepath.Join(packageRoot, filepath.FromSlash(single))}
	}

	var entries map[string]string
	if err := json.Unmarshal(manifest.Bin, &entries); err != nil {
		return nil
	}
	entry := strings.TrimSpace(entries[command])
	if entry == "" {
		return nil
	}
	return []string{filepath.Join(packageRoot, filepath.FromSlash(entry))}
}
