package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testTemplate = `{
    "TemporalHost": "${UNITMAN_MONO_HOST:-localhost}:7233",
    "ApiHost": "http://${UNITMAN_MONO_HOST:-localhost}:8082"
}
`

func TestRenderConfigReplacesHostEverywhere(t *testing.T) {
	rendered, err := RenderConfig([]byte(testTemplate), "192.168.1.25")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var configuration Configuration
	if err := json.Unmarshal(rendered, &configuration); err != nil {
		t.Fatalf("rendered config is not valid JSON: %v\n%s", err, rendered)
	}

	if configuration.TemporalHost != "192.168.1.25:7233" {
		t.Fatalf("unexpected TemporalHost: %q", configuration.TemporalHost)
	}
	if configuration.ApiHost != "http://192.168.1.25:8082" {
		t.Fatalf("unexpected ApiHost: %q", configuration.ApiHost)
	}
	if strings.Contains(string(rendered), "${UNITMAN_MONO_HOST") {
		t.Fatalf("placeholder was not replaced:\n%s", rendered)
	}
}

func TestRenderConfigDefaultsToLocalhost(t *testing.T) {
	rendered, err := RenderConfig([]byte(testTemplate), "  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(rendered), `"TemporalHost": "localhost:7233"`) {
		t.Fatalf("unexpected rendered config:\n%s", rendered)
	}
}

func TestRenderConfigRejectsInvalidHost(t *testing.T) {
	for _, host := range []string{`bad"host`, "bad host", "bad\\host"} {
		if _, err := RenderConfig([]byte(testTemplate), host); err == nil {
			t.Fatalf("expected error for host %q", host)
		}
	}
}

func TestRenderConfigRequiresPlaceholder(t *testing.T) {
	if _, err := RenderConfig([]byte(`{"TemporalHost":"localhost:7233"}`), "localhost"); err == nil {
		t.Fatal("expected error when template has no host placeholder")
	}
}

func TestCreateConfigWritesRenderedFile(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, ConfigTemplatePath)
	targetPath := filepath.Join(dir, ConfigPath)

	if err := os.WriteFile(templatePath, []byte(testTemplate), 0644); err != nil {
		t.Fatalf("cannot write template: %v", err)
	}

	if err := CreateConfig(templatePath, targetPath, "runner.local"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("cannot read config: %v", err)
	}
	if !strings.Contains(string(data), "runner.local:7233") || !strings.Contains(string(data), "http://runner.local:8082") {
		t.Fatalf("unexpected config:\n%s", data)
	}
}
