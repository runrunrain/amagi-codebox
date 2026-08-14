package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/config"
	"gopkg.in/yaml.v3"
)

// ManagedProviderPrefix is the namespace CodeBox owns inside harness config
// files. Entries outside this namespace belong to the user or to a harness
// authentication flow and must never be changed by provider synchronization.
const ManagedProviderPrefix = "amagi-"

// ManagedProviderModel is the harness-neutral model information collected from
// a provider's default model and the presets that reference it.
type ManagedProviderModel struct {
	ID         string
	Parameters config.Parameters
}

// ManagedProviderID returns the stable, collision-resistant provider id used
// for persistent harness configuration.
func ManagedProviderID(providerName string) string {
	return ManagedProviderPrefix + providerName
}

// BuildPiManagedProviderConfig builds one Pi provider containing every model
// CodeBox knows for it, instead of the single launch-time model used by the
// runtime builder.
func BuildPiManagedProviderConfig(
	providerName string,
	provider config.Provider,
	apiKey string,
	models []ManagedProviderModel,
) (map[string]any, error) {
	return buildManagedModelsConfig(providerName, provider, apiKey, models, BuildPiModelsConfig)
}

// BuildOmpManagedProviderConfig is the OMP counterpart of
// BuildPiManagedProviderConfig.
func BuildOmpManagedProviderConfig(
	providerName string,
	provider config.Provider,
	apiKey string,
	models []ManagedProviderModel,
) (map[string]any, error) {
	return buildManagedModelsConfig(providerName, provider, apiKey, models, BuildOmpModelsConfig)
}

type managedModelsBuilder func(string, config.Provider, string, string, config.Parameters) (map[string]any, error)

func buildManagedModelsConfig(
	providerName string,
	provider config.Provider,
	apiKey string,
	models []ManagedProviderModel,
	build managedModelsBuilder,
) (map[string]any, error) {
	if len(models) == 0 {
		models = []ManagedProviderModel{{ID: provider.DefaultModel}}
	}

	var providerID string
	var providerEntry map[string]any
	mergedModels := make([]map[string]any, 0, len(models))
	seenModels := make(map[string]struct{}, len(models))
	for _, model := range models {
		cfg, err := build(providerName, provider, model.ID, apiKey, model.Parameters)
		if err != nil {
			return nil, err
		}
		entries := piProviderEntries(cfg["providers"])
		for id, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("provider %q generated an invalid harness entry", providerName)
			}
			if providerEntry == nil {
				providerID = id
				providerEntry = entry
			}
			for _, generatedModel := range modelEntryList(entry["models"]) {
				id, _ := generatedModel["id"].(string)
				if _, exists := seenModels[id]; id == "" || exists {
					continue
				}
				seenModels[id] = struct{}{}
				mergedModels = append(mergedModels, generatedModel)
			}
		}
	}
	if providerEntry == nil {
		return nil, fmt.Errorf("provider %q generated no harness entry", providerName)
	}
	if len(mergedModels) > 0 {
		providerEntry["models"] = mergedModels
	} else {
		delete(providerEntry, "models")
	}
	return map[string]any{
		"providers": map[string]any{providerID: providerEntry},
	}, nil
}

func modelEntryList(value any) []map[string]any {
	switch entries := value.(type) {
	case []map[string]any:
		return entries
	case []any:
		out := make([]map[string]any, 0, len(entries))
		for _, rawEntry := range entries {
			if entry, ok := rawEntry.(map[string]any); ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		return nil
	}
}

// BuildOpenCodeManagedProviderEntry builds a persistent OpenCode custom
// provider entry. Persistent entries deliberately always use the amagi-
// namespace instead of OpenCode's built-in ids (openai, anthropic, ...), so a
// /connect credential or another user-managed provider can never be replaced.
func BuildOpenCodeManagedProviderEntry(
	providerName string,
	provider config.Provider,
	apiKey string,
	models []ManagedProviderModel,
) (string, map[string]any, error) {
	format := provider.PreferredFormat()
	baseURL := strings.TrimSpace(provider.EffectiveBaseURL(format))
	if baseURL == "" {
		return "", nil, fmt.Errorf("provider %q has no baseURL, cannot build opencode config", providerName)
	}

	entry := map[string]any{
		"name": providerName,
	}
	options := map[string]any{
		"baseURL": baseURL,
	}
	if apiKey != "" {
		options["apiKey"] = apiKey
	}
	if headers := resolveEnvHeaders(provider.EffectiveHeaders(format)); len(headers) > 0 {
		options["headers"] = headers
	}

	switch format {
	case "openai":
		entry["npm"] = "@ai-sdk/openai-compatible"
		if provider.OpenAI != nil && strings.TrimSpace(provider.OpenAI.Organization) != "" {
			options["organization"] = provider.OpenAI.Organization
		}
	default:
		entry["npm"] = "@ai-sdk/anthropic"
	}
	entry["options"] = options

	modelEntries := make(map[string]any, len(models))
	for _, model := range models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		modelEntry := map[string]any{"name": modelID}
		limit := map[string]any{}
		if model.Parameters.ContextWindow != nil && model.Parameters.ContextWindow.ModelContextWindow > 0 {
			limit["context"] = model.Parameters.ContextWindow.ModelContextWindow
		}
		if model.Parameters.MaxTokens > 0 {
			limit["output"] = model.Parameters.MaxTokens
		}
		if len(limit) > 0 {
			modelEntry["limit"] = limit
		}
		if modelOptions := buildOpenCodeModelOptions(config.Preset{Parameters: model.Parameters}); len(modelOptions) > 0 {
			modelEntry["options"] = modelOptions
		}
		modelEntries[modelID] = modelEntry
	}
	if len(modelEntries) > 0 {
		entry["models"] = modelEntries
	}

	return ManagedProviderID(providerName), entry, nil
}

// mergeProviderConfig combines one harness config with another. Reconciliation
// prunes the whole managed namespace; launch-time overlays preserve sibling
// amagi providers so launching one model cannot undo the unified provider sync.
func mergeProviderConfig(existing, desired map[string]any, pruneManaged bool) map[string]any {
	existingProviders := piProviderEntries(existing["providers"])
	desiredProviders := piProviderEntries(desired["providers"])
	providers := make(map[string]any, len(existingProviders)+len(desiredProviders))
	for key, value := range existingProviders {
		if pruneManaged && strings.HasPrefix(key, ManagedProviderPrefix) {
			continue
		}
		providers[key] = value
	}
	for key, value := range desiredProviders {
		providers[key] = value
	}

	merged := make(map[string]any, len(existing)+len(desired))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range desired {
		if key == "providers" {
			continue
		}
		merged[key] = value
	}
	_, existingHasProviders := existing["providers"]
	_, desiredHasProviders := desired["providers"]
	if existingHasProviders || desiredHasProviders || len(providers) > 0 {
		merged["providers"] = providers
	}
	return merged
}

// ReconcilePiAgentConfig strictly parses the existing Pi models.json and
// replaces only amagi-* providers. Invalid existing content is returned as an
// error instead of being overwritten.
func ReconcilePiAgentConfig(cfg map[string]any, agentDir string) error {
	if strings.TrimSpace(agentDir) == "" {
		return errors.New("pi agentDir is required")
	}
	path := filepath.Join(agentDir, "models.json")
	existing, err := readStrictJSONObject(path)
	if err != nil {
		return fmt.Errorf("read existing pi models.json: %w", err)
	}
	return WritePiAgentConfig(agentDir, mergeProviderConfig(existing, cfg, true))
}

// ReconcileOmpAgentConfig strictly parses the existing OMP models.yml and
// replaces only amagi-* providers. Invalid existing content is returned as an
// error instead of being overwritten.
func ReconcileOmpAgentConfig(cfg map[string]any, agentDir string) error {
	if strings.TrimSpace(agentDir) == "" {
		return errors.New("omp agentDir is required")
	}
	path := filepath.Join(agentDir, "models.yml")
	existing, err := readStrictYAMLObject(path)
	if err != nil {
		return fmt.Errorf("read existing omp models.yml: %w", err)
	}
	return WriteOmpAgentConfig(agentDir, mergeProviderConfig(existing, cfg, true))
}

func readStrictJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, errors.New("file is empty")
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("root must be an object")
	}
	if providers, ok := obj["providers"]; ok && providers != nil && len(piProviderEntries(providers)) == 0 {
		if _, isEmptyObject := providers.(map[string]any); !isEmptyObject {
			return nil, errors.New("providers must be an object")
		}
	}
	return obj, nil
}

func readStrictYAMLObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, errors.New("file is empty")
	}
	var obj map[string]any
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("root must be an object")
	}
	if providers, ok := obj["providers"]; ok && providers != nil {
		if _, isObject := providers.(map[string]any); !isObject {
			return nil, errors.New("providers must be an object")
		}
	}
	return obj, nil
}
