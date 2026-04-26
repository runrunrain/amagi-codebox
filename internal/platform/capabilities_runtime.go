package platform

import "runtime"

func CurrentCapabilities() PlatformCapabilities {
	return capabilitiesForTarget(runtime.GOOS, runtime.GOARCH)
}

func capabilitiesForTarget(osName string, arch string) PlatformCapabilities {
	platformID := osName
	if osName == "darwin" {
		platformID = "darwin-" + arch
	}

	capabilities := PlatformCapabilities{
		PlatformID:               platformID,
		OS:                       osName,
		Arch:                     arch,
		FileOpenSupported:        true,
		UpdateCheckSupported:     true,
		AutoStartSupported:       false,
		PathDiagnosticsSupported: true,
	}

	switch osName {
	case "windows":
		capabilities.EmbeddedTerminalSupported = true
		capabilities.StandaloneTerminalSupported = true
		capabilities.SystemTraySupported = true
		capabilities.UpdateInstallSupported = true
		capabilities.SingleInstanceSupported = true
		capabilities.WindowActivationSupported = true
		capabilities.HideOnCloseSupported = true
		capabilities.BackgroundResidentSupported = true
		capabilities.CloseAction = CloseActionHide
		capabilities.SecureSecretStoreKind = "dpapi"
		capabilities.DefaultShellKey = "pwsh"
	case "darwin":
		capabilities.EmbeddedTerminalSupported = true
		capabilities.StandaloneTerminalSupported = false
		capabilities.SystemTraySupported = false
		capabilities.UpdateInstallSupported = false
		capabilities.SingleInstanceSupported = false
		capabilities.WindowActivationSupported = false
		capabilities.HideOnCloseSupported = false
		capabilities.BackgroundResidentSupported = false
		capabilities.CloseAction = CloseActionQuit
		capabilities.SecureSecretStoreKind = "keychain"
		capabilities.DefaultShellKey = "zsh"
	}

	capabilities.SupportedShells = defaultShellCatalog(capabilities)
	return capabilities
}
