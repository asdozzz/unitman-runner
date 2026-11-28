package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedAssetsContainRequiredFiles(t *testing.T) {
	required := []string{
		"temporal/activity/unit/docker-dind/compose.dind.yaml",
		"temporal/activity/unit/docker-dind/compose.ide-view.yaml",
		"temporal/activity/unit/docker-dind/compose.ide-edit.yaml",
		"temporal/activity/unit/docker-dind/entrypoint.sh",
		"temporal/activity/unit/docker-dind/unit-skills-sync.sh",
		"temporal/activity/workspace/docker-dind/compose.dind.yaml",
		"temporal/activity/workspace/docker-dind/Dockerfile.temp",
		"temporal/activity/workspace/docker-dind/unit-skills-sync.sh",
		".docker/proxy/nginx.conf",
	}

	for _, path := range required {
		if _, err := embeddedAssets.ReadFile(path); err != nil {
			t.Fatalf("embedded asset %q is missing: %v", path, err)
		}
	}
}

func TestExtractEmbeddedAssets(t *testing.T) {
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	if err := extractEmbeddedAssets(); err != nil {
		t.Fatalf("extractEmbeddedAssets: %v", err)
	}

	compose := filepath.Join(dir, "temporal/activity/unit/docker-dind/compose.dind.yaml")
	if _, err := os.Stat(compose); err != nil {
		t.Fatalf("compose template was not extracted: %v", err)
	}

	script := filepath.Join(dir, "temporal/activity/unit/docker-dind/entrypoint.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("entrypoint script was not extracted: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("entrypoint script must be executable, got %v", info.Mode())
	}

	proxy := filepath.Join(dir, ".docker/proxy/nginx.conf")
	if _, err := os.Stat(proxy); err != nil {
		t.Fatalf("proxy config was not extracted: %v", err)
	}
}

func TestComposeFilesMountSkillsReadOnly(t *testing.T) {
	cases := []string{
		"temporal/activity/unit/docker-dind/compose.dind.yaml",
		"temporal/activity/workspace/docker-dind/compose.dind.yaml",
	}

	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			raw, err := embeddedAssets.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if !strings.Contains(string(raw), "./seed/skills:/opt/unitman/skills:ro") {
				t.Fatalf("%s must mount ./seed/skills read-only:\n%s", path, raw)
			}
		})
	}
}

func TestEntrypointsRunSkillsSync(t *testing.T) {
	cases := []string{
		"temporal/activity/unit/docker-dind/entrypoint.sh",
		"temporal/activity/workspace/docker-dind/entrypoint.sh",
	}

	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			raw, err := embeddedAssets.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if !strings.Contains(string(raw), "unit-skills-sync || true") {
				t.Fatalf("%s must call unit-skills-sync best-effort:\n%s", path, raw)
			}
		})
	}
}
