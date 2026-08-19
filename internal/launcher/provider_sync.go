package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

type managedModelsBuilder func(string, config.Provider, string, string, config.Parameters, []ManagedProviderModel) (map[string]any, error)

// ManagedPresetModels derives the model registrations CodeBox publishes for
// one provider from its DefaultModel, its legacy provider.Presets and the
// terminal preset buckets listed in terminalTypes. Later sources overwrite
// earlier ones for the same model id, so bucket presets (iterated in the
// given order) win over legacy presets and the zero-parameter DefaultModel.
// The result is sorted by model id, making it deterministic and directly
// consumable as the presetModels argument of BuildPiModelsConfig /
// BuildOmpModelsConfig. Pi and OMP must pass only the OpenAI bucket
// (TerminalPresetOpenAI): the Anthropic bucket belongs to Claude Code.
func ManagedPresetModels(
	providerName string,
	provider config.Provider,
	presets *config.TerminalPresetsConfig,
	terminalTypes ...config.TerminalPresetType,
) []ManagedProviderModel {
	modelParams := map[string]config.Parameters{}
	if model := strings.TrimSpace(provider.DefaultModel); model != "" {
		modelParams[model] = config.Parameters{}
	}
	for _, key := range sortedPresetKeys(provider.Presets) {
		if model := strings.TrimSpace(provider.Presets[key].Model); model != "" {
			modelParams[model] = provider.Presets[key].Parameters
		}
	}
	for _, terminalType := range terminalTypes {
		presetMap := presets.GetMap(terminalType)
		for _, key := range sortedTerminalPresetKeys(presetMap) {
			preset := presetMap[key]
			if strings.TrimSpace(preset.Provider) != providerName {
				continue
			}
			for _, model := range []string{preset.Model, preset.ModelHaiku, preset.ModelSonnet, preset.ModelOpus} {
				if model = strings.TrimSpace(model); model != "" {
					modelParams[model] = preset.Parameters
				}
			}
		}
	}

	modelNames := make([]string, 0, len(modelParams))
	for model := range modelParams {
		modelNames = append(modelNames, model)
	}
	sort.Strings(modelNames)
	models := make([]ManagedProviderModel, 0, len(modelNames))
	for _, model := range modelNames {
		models = append(models, ManagedProviderModel{ID: model, Parameters: modelParams[model]})
	}
	return models
}

// sortedTerminalPresetKeys returns terminal preset keys in sorted order
// (nil-safe), keeping bucket iteration deterministic.
func sortedTerminalPresetKeys(presetMap map[string]config.TerminalPreset) []string {
	keys := make([]string, 0, len(presetMap))
	for key := range presetMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

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

	// Single-pass registration: the whole models list is handed to the builder
	// so every collected model keeps its own parameters. The previous shape
	// invoked the builder once per model and merged entries with a first-seen
	// dedup; earlier passes appended their trailing bare DefaultModel
	// registration, which won the dedup and stripped the collected preset
	// parameters of the default model (observed: glm-5.3 written bare while its
	// openai preset carried contextWindow/maxTokens/reasoning).
	launched := models[0]
	return build(providerName, provider, launched.ID, apiKey, launched.Parameters, models)
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
