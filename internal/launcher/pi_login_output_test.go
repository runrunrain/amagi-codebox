package launcher

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPiLoginOutputOverrideLifecycle(t *testing.T) {
	dir := t.TempDir()
	writeOverrideFixture(t, dir, `{"providers":{"p":{"modelOverrides":{"m":{"contextWindow":100000,"compat":{"supportsStrictTools":false}},"sibling":{"maxTokens":999}}}}}`)
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 200000, 65536); err != nil {
		t.Fatal(err)
	}
	key := overrideEntry(t, readOverrideProviders(t, dir), "p", "m")
	if key["maxTokens"] != float64(65536) || key["contextWindow"] != float64(200000) || key["compat"] == nil {
		t.Fatalf("override: %+v", key)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 200000, 65536); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent save changed file")
	}
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 200000, 0); err != nil {
		t.Fatal(err)
	}
	key = overrideEntry(t, readOverrideProviders(t, dir), "p", "m")
	if _, ok := key["maxTokens"]; ok {
		t.Fatal("output not removed")
	}
	if key["contextWindow"] != float64(200000) {
		t.Fatal("clearing output lost window")
	}
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 0, 0); err != nil {
		t.Fatal(err)
	}
	key = overrideEntry(t, readOverrideProviders(t, dir), "p", "m")
	if len(key) != 1 || key["compat"] == nil {
		t.Fatalf("unrelated fields lost: %+v", key)
	}
	if overrideEntry(t, readOverrideProviders(t, dir), "p", "sibling")["maxTokens"] != float64(999) {
		t.Fatal("sibling changed")
	}
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 0, -1); err == nil {
		t.Fatal("negative output accepted")
	}
}

func TestPiLoginOutputOnlyCreatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 0, 32768); err != nil {
		t.Fatal(err)
	}
	if overrideEntry(t, readOverrideProviders(t, dir), "p", "m")["maxTokens"] != float64(32768) {
		t.Fatal("output-only override missing")
	}
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := readOverrideProviders(t, dir)["p"]; ok {
		t.Fatal("empty provider not pruned")
	}
	writeOverrideFixture(t, dir, `{"providers":{"p":{"modelOverrides":{"m":"broken"}}}}`)
	before, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if err := UpsertPiLoginModelOverride(dir, "p", "m", 0, 32768); err == nil {
		t.Fatal("corrupt structure accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt file overwritten")
	}
}
