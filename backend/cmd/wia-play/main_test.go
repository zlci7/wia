package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelConfigUsesFormalApplicationPath(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("WIA_MODEL_CONFIG", "")
	if !strings.HasSuffix(modelConfigPath(), filepath.Join("WorldIsAgent", "story-app", "config", "model.json")) {
		t.Fatal("play study did not use formal application configuration")
	}
	t.Setenv("WIA_MODEL_CONFIG", "custom-model.json")
	if modelConfigPath() != "custom-model.json" {
		t.Fatal("explicit configured override ignored")
	}
}

func TestStudyInputRejectsDuplicateUnknownAndEmpty(t *testing.T) {
	for _, text := range []string{
		`[{"input":"一","input":"二"}]`,
		`[{"input":"一","execute":"shell"}]`,
		`[{"input":""}]`,
		`[]`,
	} {
		path := filepath.Join(t.TempDir(), "inputs.json")
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadStudyInputs(path); err == nil {
			t.Fatalf("invalid study accepted: %s", text)
		}
	}
}
