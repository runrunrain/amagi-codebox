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
		capabilities.SystemProxyControlSupported = true
		capabilities.CloseAction = CloseActionHide
		capabilities.SecureSecretStoreKind = "dpapi"
		// Default to WSL so terminals launched by CodeBox run in a Linux
		// environment, sidestepping PowerShell-specific friction. When no usable
		// WSL distro is installed the resolver falls back to pwsh/powershell/cmd
		// via defaultShellForCapabilities' two-pass candidate scan.
		capabilities.DefaultShellKey = "wsl"
	case "darwin":
		capabilities.EmbeddedTerminalSupported = true
		capabilities.StandaloneTerminalSupported = false
		capabilities.SystemTraySupported = false
		capabilities.UpdateInstallSupported = arch == "arm64"
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
