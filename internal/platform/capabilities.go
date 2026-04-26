package platform

type CloseAction string

const (
	CloseActionHide CloseAction = "hide"
	CloseActionQuit CloseAction = "quit"
)

type ShellDescriptor struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	ResolvedPath string `json:"resolvedPath"`
	Available    bool   `json:"available"`
	IsDefault    bool   `json:"isDefault"`
}

type PlatformCapabilities struct {
	PlatformID                  string            `json:"platformId"`
	OS                          string            `json:"os"`
	Arch                        string            `json:"arch"`
	EmbeddedTerminalSupported   bool              `json:"embeddedTerminalSupported"`
	StandaloneTerminalSupported bool              `json:"standaloneTerminalSupported"`
	SystemTraySupported         bool              `json:"systemTraySupported"`
	FileOpenSupported           bool              `json:"fileOpenSupported"`
	UpdateCheckSupported        bool              `json:"updateCheckSupported"`
	UpdateInstallSupported      bool              `json:"updateInstallSupported"`
	AutoStartSupported          bool              `json:"autoStartSupported"`
	SingleInstanceSupported     bool              `json:"singleInstanceSupported"`
	WindowActivationSupported   bool              `json:"windowActivationSupported"`
	HideOnCloseSupported        bool              `json:"hideOnCloseSupported"`
	BackgroundResidentSupported bool              `json:"backgroundResidentSupported"`
	CloseAction                 CloseAction       `json:"closeAction"`
	SecureSecretStoreKind       string            `json:"secureSecretStoreKind"`
	PathDiagnosticsSupported    bool              `json:"pathDiagnosticsSupported"`
	SupportedShells             []ShellDescriptor `json:"supportedShells"`
	DefaultShellKey             string            `json:"defaultShellKey"`
}

type CapabilityViolation struct {
	Code             string `json:"code"`
	Message          string `json:"message"`
	PlatformID       string `json:"platformId"`
	RequestedFeature string `json:"requestedFeature"`
	SuggestedAction  string `json:"suggestedAction,omitempty"`
}

func (v *CapabilityViolation) Error() string {
	if v == nil {
		return ""
	}
	return v.Message
}

func ValidateLaunchRequest(capabilities PlatformCapabilities, launchMode string) error {
	switch launchMode {
	case "", "embedded":
		if !capabilities.EmbeddedTerminalSupported {
			return &CapabilityViolation{
				Code:             "feature_disabled_on_platform",
				Message:          "embedded terminal is not supported on this platform",
				PlatformID:       capabilities.PlatformID,
				RequestedFeature: "embedded-terminal",
			}
		}
		return nil
	case "terminal":
		if !capabilities.StandaloneTerminalSupported {
			return &CapabilityViolation{
				Code:             "unsupported_launch_mode",
				Message:          "standalone terminal launch is not supported on this platform",
				PlatformID:       capabilities.PlatformID,
				RequestedFeature: "standalone-terminal",
				SuggestedAction:  "use embedded mode instead",
			}
		}
		return nil
	default:
		return &CapabilityViolation{
			Code:             "unsupported_launch_mode",
			Message:          "unsupported launch mode: " + launchMode,
			PlatformID:       capabilities.PlatformID,
			RequestedFeature: launchMode,
		}
	}
}
