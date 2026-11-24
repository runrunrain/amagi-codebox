package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"amagi-codebox/internal/config"
)

func TestPiLoginVisionRestoresOriginalInput(t *testing.T) {
	for _, tc := range []struct {
		name, fields  string
		during, after any
		ownsInput     bool
	}{
		{"directory default", ``, []any{"text", "image"}, nil, true},
		{"explicit text", `,"input":["text"]`, []any{"text", "image"}, []any{"text"}, true},
		{"manual vision already present", `,"input":["text","image"]`, []any{"text", "image"}, []any{"text", "image"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeOverrideFixture(t, dir, `{"providers":{"p":{"modelOverrides":{"m":{"compat":{"supportsStrictTools":false}`+tc.fields+`}}}}}`)
			p := config.PiLoginPreset{Provider: "p", Model: "m", Vision: true, ContextWindow: 256000}
			mustApply := func() {
				t.Helper()
				if err := ApplyPiLoginPresetModelOverride(dir, p); err != nil {
					t.Fatal(err)
				}
			}
			read := func(want any, managed bool) {
				t.Helper()
				root, err := readStrictJSONObject(filepath.Join(dir, "models.json"))
				if err != nil {
					t.Fatal(err)
				}
				entry := overrideEntry(t, piProviderEntries(root["providers"]), "p", "m")
				if !reflect.DeepEqual(entry["input"], want) || entry["compat"].(map[string]any)["supportsStrictTools"] != false || entry["contextWindow"] != float64(256000) {
					t.Fatalf("unexpected model fields: %#v", entry)
				}
				if _, ok := root[piLoginInputOwnershipKey]; ok != managed {
					t.Fatalf("input ownership exists=%v, want %v", ok, managed)
				}
			}
			mustApply()
			read(tc.during, tc.ownsInput)
			mustApply() // must not replace the backup with the value CodeBox wrote
			p.Vision = false
			mustApply()
			read(tc.after, false)
		})
	}
}

func TestPiLoginVisionDisablePreservesExternalEdit(t *testing.T) {
	dir := t.TempDir()
	p := config.PiLoginPreset{Provider: "p", Model: "m", Vision: true}
	if err := ApplyPiLoginPresetModelOverride(dir, p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "models.json")
	root, err := readStrictJSONObject(path)
	if err != nil {
		t.Fatal(err)
	}
	entry := overrideEntry(t, piProviderEntries(root["providers"]), "p", "m")
	entry["input"] = []string{"text"}
	if err := WritePiAgentConfig(dir, root); err != nil {
		t.Fatal(err)
	}
	p.Vision = false
	if err := ApplyPiLoginPresetModelOverride(dir, p); err != nil {
		t.Fatal(err)
	}
	root, err = readStrictJSONObject(path)
	if err != nil {
		t.Fatal(err)
	}
	entry = overrideEntry(t, piProviderEntries(root["providers"]), "p", "m")
	if got := entry["input"]; !reflect.DeepEqual(got, []any{"text"}) {
		t.Fatalf("clearing CodeBox vision removed an external edit: %#v", got)
	}
	if _, ok := root[piLoginInputOwnershipKey]; ok {
		t.Fatal("ownership should be relinquished")
	}
}

func TestPiLoginVisionLegacyDefaultLeavesManualInputUntouched(t *testing.T) {
	dir := t.TempDir()
	original := `{"providers":{"p":{"modelOverrides":{"m":{"input":["text","image"]}}}}}`
	writeOverrideFixture(t, dir, original)
	if err := ApplyPiLoginPresetModelOverride(dir, config.PiLoginPreset{Provider: "p", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil || string(data) != original {
		t.Fatalf("legacy preset modified Pi's original input: %s (%v)", data, err)
	}
}

func TestPiLoginVisionRejectsMalformedInput(t *testing.T) {
	dir := t.TempDir()
	original := `{"providers":{"p":{"modelOverrides":{"m":{"input":42}}}}}`
	writeOverrideFixture(t, dir, original)
	if err := ApplyPiLoginPresetModelOverride(dir, config.PiLoginPreset{Provider: "p", Model: "m", Vision: true}); err == nil {
		t.Fatal("malformed input was overwritten")
	}
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil || string(data) != original {
		t.Fatalf("malformed Pi config was changed: %v", err)
	}
}
