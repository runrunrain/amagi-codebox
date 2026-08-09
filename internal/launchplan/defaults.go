package launchplan

import (
	"errors"

	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
)

// DefaultStore is the launchplan-facing adapter over settings.json's
// remoteLaunchDefaultsV1 map. The settings value contains stable references and
// booleans only; workdir and all ephemeral launch material are resolved later.
type DefaultStore struct {
	settings *settings.Service
}

func NewDefaultStore(service *settings.Service) (*DefaultStore, error) {
	if service == nil {
		return nil, errors.New("launchplan: settings service is required")
	}
	return &DefaultStore{settings: service}, nil
}

// RecordDesktopActivation persists the stable refs only after the caller has
// committed desktop Authority activation. Remote and restart paths do not call
// this method.
func (s *DefaultStore) RecordDesktopActivation(recipe StableRecipe) error {
	if s == nil || s.settings == nil || !KnownCLIType(recipe.CLIType) {
		return ErrInvalidPlan
	}
	return s.settings.RecordRemoteLaunchDefaultV1(string(recipe.CLIType), settings.RemoteLaunchDefaultV1{
		ProviderRef: recipe.ProviderRef,
		PresetRef:   recipe.PresetRef,
		ModelRef:    recipe.ModelRef,
		ShellRef:    recipe.ShellRef,
		UseProxy:    recipe.UseProxy,
		UseHeadroom: recipe.UseHeadroom,
	})
}

func (s *DefaultStore) HostDefaultRefs(cli contract.CLIType) (StableRecipe, bool) {
	if s == nil || s.settings == nil || !KnownCLIType(cli) {
		return StableRecipe{}, false
	}
	refs, ok := s.settings.GetRemoteLaunchDefaultsV1()[string(cli)]
	if !ok {
		return StableRecipe{}, false
	}
	return StableRecipe{
		CLIType:     cli,
		ProviderRef: refs.ProviderRef,
		PresetRef:   refs.PresetRef,
		ModelRef:    refs.ModelRef,
		ShellRef:    refs.ShellRef,
		UseProxy:    refs.UseProxy,
		UseHeadroom: refs.UseHeadroom,
	}, true
}
