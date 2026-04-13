package workspace

import (
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/plugin"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	configDir          string
	workspacesPath     string
	globalEnabledPath  string
	globalManifestPath string
	homeDir            string
	plugins            *plugin.Service
	log                *logging.Service
	mu                 sync.RWMutex
	workspaces         []Workspace
	globalEnabled      []GlobalEnabled
}

func NewService(configDir string, plugins *plugin.Service, log *logging.Service) *Service {
	homeDir, _ := os.UserHomeDir()
	return &Service{
		configDir:          configDir,
		workspacesPath:     filepath.Join(configDir, "workspaces.json"),
		globalEnabledPath:  filepath.Join(configDir, "global-enabled.json"),
		globalManifestPath: filepath.Join(configDir, "global-deploy-manifest.json"),
		homeDir:            homeDir,
		plugins:            plugins,
		log:                log,
		workspaces:         []Workspace{},
		globalEnabled:      []GlobalEnabled{},
	}
}

func (s *Service) ListWorkspaces() []Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneWorkspaces(s.workspaces)
}

func (s *Service) GetWorkspace(id string) (Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspace, _, err := s.findWorkspaceLocked(id)
	if err != nil {
		return Workspace{}, err
	}
	return cloneWorkspaces([]Workspace{workspace})[0], nil
}

func (s *Service) CreateWorkspace(name, path string, tools []ToolType) (Workspace, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspace := normalizeWorkspace(Workspace{ID: uuid.NewString(), Name: name, Path: path, Tools: tools, Plugins: []WorkspacePlugin{}, CreatedAt: now, UpdatedAt: now})
	if workspace.Path == "." || workspace.Path == "" {
		return Workspace{}, errors.New("workspace path is required")
	}
	if workspace.Name == "" {
		workspace.Name = filepath.Base(workspace.Path)
	}
	if len(workspace.Tools) == 0 {
		workspace.Tools = []ToolType{ToolTypeClaude}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.workspaces {
		if item.Path == workspace.Path {
			return Workspace{}, fmt.Errorf("workspace already exists for path %s", workspace.Path)
		}
	}
	s.workspaces = append(s.workspaces, workspace)
	if err := writeJSONFile(s.workspacesPath, workspacesFile{Workspaces: s.workspaces}); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func (s *Service) UpdateWorkspace(id, name, path string, tools []ToolType) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, index, err := s.findWorkspaceLocked(id)
	if err != nil {
		return Workspace{}, err
	}
	updated := workspace
	updated.Name = name
	updated.Path = path
	updated.Tools = tools
	updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	updated = normalizeWorkspace(updated)
	if updated.Name == "" {
		updated.Name = filepath.Base(updated.Path)
	}
	if len(updated.Tools) == 0 {
		updated.Tools = []ToolType{ToolTypeClaude}
	}
	for i, item := range s.workspaces {
		if i != index && item.Path == updated.Path {
			return Workspace{}, fmt.Errorf("workspace already exists for path %s", updated.Path)
		}
	}
	s.workspaces[index] = updated
	if err := writeJSONFile(s.workspacesPath, workspacesFile{Workspaces: s.workspaces}); err != nil {
		return Workspace{}, err
	}
	return updated, nil
}

func (s *Service) DeleteWorkspace(id string) error {
	if _, err := s.CleanWorkspace(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, index, err := s.findWorkspaceLocked(id)
	if err != nil {
		return err
	}
	workspace := s.workspaces[index]
	s.workspaces = append(s.workspaces[:index], s.workspaces[index+1:]...)
	if err := writeJSONFile(s.workspacesPath, workspacesFile{Workspaces: s.workspaces}); err != nil {
		return err
	}
	_ = os.Remove(ManifestPathForWorkspace(workspace.Path))
	return nil
}

func (s *Service) SetWorkspacePlugins(workspaceID string, items []WorkspacePlugin) error {
	normalized, err := s.validateWorkspacePlugins(items)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, index, err := s.findWorkspaceLocked(workspaceID)
	if err != nil {
		return err
	}
	workspace.Plugins = normalized
	workspace.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.workspaces[index] = workspace
	return writeJSONFile(s.workspacesPath, workspacesFile{Workspaces: s.workspaces})
}

func (s *Service) GetGlobalEnabled() []GlobalEnabled {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneGlobalEnabled(s.globalEnabled)
}

func (s *Service) GetDeploymentManifest(workspaceID string) (DeploymentManifest, error) {
	workspace, err := s.GetWorkspace(workspaceID)
	if err != nil {
		return DeploymentManifest{}, err
	}
	return ReadManifest(ManifestPathForWorkspace(workspace.Path))
}

func (s *Service) GetAvailablePluginsForWorkspace(workspaceID string) ([]AvailablePlugin, error) {
	workspace, err := s.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	installed, err := s.plugins.GetInstalledPlugins()
	if err != nil {
		return nil, err
	}
	globalEnabled := s.GetGlobalEnabled()
	var available []AvailablePlugin
	for _, item := range installed {
		if !item.Enabled {
			continue
		}
		detail, err := s.plugins.GetPluginDetail(item.ID)
		if err != nil {
			return nil, err
		}
		visible, updated := applyGlobalDisplayContract(*detail, workspace.Tools, globalEnabled)
		if !visible {
			continue
		}
		available = append(available, AvailablePlugin{PluginDetail: updated, GloballyEnabledAll: false})
	}
	return available, nil
}

func (s *Service) findWorkspaceLocked(id string) (Workspace, int, error) {
	for i, workspace := range s.workspaces {
		if workspace.ID == id {
			return workspace, i, nil
		}
	}
	return Workspace{}, -1, fmt.Errorf("workspace not found: %s", id)
}
