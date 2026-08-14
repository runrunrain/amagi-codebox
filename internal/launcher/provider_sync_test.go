package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReconcilePiAgentConfigDoesNotOverwriteInvalidOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	original := []byte(`{"providers":`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePiAgentConfig(map[string]any{"providers": map[string]any{}}, dir); err == nil {
		t.Fatal("expected invalid Pi config to fail reconciliation")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("invalid Pi config was overwritten: %q", got)
	}
}

func TestReconcileOmpAgentConfigDoesNotOverwriteInvalidOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yml")
	original := []byte("providers:\n  - invalid\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileOmpAgentConfig(map[string]any{"providers": map[string]any{}}, dir); err == nil {
		t.Fatal("expected invalid OMP config to fail reconciliation")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("invalid OMP config was overwritten: %q", got)
	}
}
