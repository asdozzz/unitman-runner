package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runner/config"
)

// withRegistryConfig подменяет чтение config.json на время теста и
// восстанавливает исходную функцию после его завершения.
func withRegistryConfig(t *testing.T, registryHost string) {
	t.Helper()

	previous := parseConfig
	parseConfig = func() (config.Configuration, error) {
		return config.Configuration{RunnerID: "runner-test", RegistryHost: registryHost}, nil
	}
	t.Cleanup(func() { parseConfig = previous })
}

func TestWorkspaceVolumesForNaming(t *testing.T) {
	volumes := WorkspaceVolumesFor("ws-1")
	want := map[string]string{
		"app":      "workspace_ws-1_app",
		"docker":   "workspace_ws-1_docker",
		"opencode": "workspace_ws-1_opencode",
	}
	got := map[string]string{
		"app":      volumes.App,
		"docker":   volumes.Docker,
		"opencode": volumes.OpenCode,
	}
	for name, expected := range want {
		if got[name] != expected {
			t.Fatalf("%s volume = %q, want %q", name, got[name], expected)
		}
	}
	if len(volumes.Names()) != 3 {
		t.Fatalf("expected 3 volumes, got %v", volumes.Names())
	}
}

func TestWriteWorkspaceEnvUsesContractNames(t *testing.T) {
	dir := t.TempDir()
	withRegistryConfig(t, "https://registry.example:5000/")

	command := ZapustitWorkspaceCommand{
		WorkspaceId:   "ws-1",
		WorkspaceName: "payments refactor",
		WorkspaceSlug: "payments-refactor-1a2b3c4d",
		Branch:        "feature/payments",
		ReadOnly:      true,
		MemoryLimit:   3072,
	}

	if err := writeWorkspaceEnv(dir, command); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("cannot read .env: %v", err)
	}

	content := string(data)
	for _, want := range []string{
		"UNITMAN_WORKSPACE_ID=ws-1",
		"UNITMAN_WORKSPACE_NAME=payments refactor",
		"UNITMAN_WORKSPACE_SLUG=payments-refactor-1a2b3c4d",
		"UNITMAN_BRANCH_NAME=feature/payments",
		"UNITMAN_READ_ONLY=true",
		"UNITMAN_MEMORY_LIMIT=3072m",
		// Канонический вид: без завершающего слэша, со схемой.
		"UNITMAN_REGISTRY_HOST=https://registry.example:5000",
		"COMPOSE_PROJECT_NAME=workspace_ws-1",
		"UNITMAN_WORKSPACE_APP_VOLUME=workspace_ws-1_app",
		"UNITMAN_WORKSPACE_DOCKER_VOLUME=workspace_ws-1_docker",
		"UNITMAN_WORKSPACE_OPENCODE_VOLUME=workspace_ws-1_opencode",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf(".env does not contain %q:\n%s", want, content)
		}
	}
}

func TestWriteWorkspaceEnvDefaultsMemory(t *testing.T) {
	dir := t.TempDir()
	withRegistryConfig(t, "registry.example:5000")

	command := ZapustitWorkspaceCommand{WorkspaceId: "ws-1"}

	if err := writeWorkspaceEnv(dir, command); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(data), "UNITMAN_MEMORY_LIMIT=3072m") {
		t.Fatalf("default memory limit is missing:\n%s", data)
	}
	if !strings.Contains(string(data), "UNITMAN_REGISTRY_HOST=http://registry.example:5000") {
		t.Fatalf("normalized registry host is missing:\n%s", data)
	}
}

func TestWriteWorkspaceEnvRequiresRegistryHost(t *testing.T) {
	dir := t.TempDir()
	withRegistryConfig(t, "")

	err := writeWorkspaceEnv(dir, ZapustitWorkspaceCommand{WorkspaceId: "ws-1"})
	if err == nil {
		t.Fatal("expected error when RegistryHost is empty")
	}
	if !strings.Contains(err.Error(), "registryHost") {
		t.Fatalf("error should mention registryHost: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(statErr) {
		t.Fatalf(".env must not be written when config is invalid")
	}
}

func TestValidateWorkspaceIDRejectsTraversal(t *testing.T) {
	for _, value := range []string{"", ".", "..", "a/b", `a\b`} {
		if err := validateWorkspaceID(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}

	if err := validateWorkspaceID("ws-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePorcelain(t *testing.T) {
	files := parsePorcelain(" M src/Foo.php\n?? new.txt\n\n")
	if len(files) != 2 {
		t.Fatalf("unexpected files: %+v", files)
	}
	if files[0] != " M src/Foo.php" || files[1] != "?? new.txt" {
		t.Fatalf("unexpected order: %+v", files)
	}

	if empty := parsePorcelain("  \n\n"); empty == nil || len(empty) != 0 {
		t.Fatalf("empty status must produce empty slice: %#v", empty)
	}
}

func envEntry(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return entry, true
		}
	}
	return "", false
}

func TestBuildRepoEnvIncludesBaseBranch(t *testing.T) {
	command := ZapustitWorkspaceCommand{Branch: "feature/test"}
	project := Project{
		ProjectId:   "project-1",
		ProjectCode: "api",
		StorageUrl:  "https://example.com/services/api.git",
		CanPush:     true,
		MainBranch:  "main",
	}

	env := buildRepoEnv(command, project)

	if got, ok := envEntry(env, "UNITMAN_REPO_BASE_BRANCH"); !ok || got != "UNITMAN_REPO_BASE_BRANCH=main" {
		t.Fatalf("base branch env = %q (present=%v), want UNITMAN_REPO_BASE_BRANCH=main: %v", got, ok, env)
	}
	if got, ok := envEntry(env, "UNITMAN_REPO_BRANCH"); !ok || got != "UNITMAN_REPO_BRANCH=feature/test" {
		t.Fatalf("branch env = %q (present=%v), want UNITMAN_REPO_BRANCH=feature/test: %v", got, ok, env)
	}
}

func TestBuildRepoEnvOmitsEmptyBaseBranch(t *testing.T) {
	env := buildRepoEnv(ZapustitWorkspaceCommand{Branch: "feature/test"}, Project{ProjectId: "project-1"})

	if got, ok := envEntry(env, "UNITMAN_REPO_BASE_BRANCH"); ok {
		t.Fatalf("empty base branch must not be exported, got %q: %v", got, env)
	}
	if got, ok := envEntry(env, "UNITMAN_REPO_BRANCH"); !ok || got != "UNITMAN_REPO_BRANCH=feature/test" {
		t.Fatalf("branch env = %q (present=%v): %v", got, ok, env)
	}
}

func TestErrorKeyFromOutput(t *testing.T) {
	output := "git clone failed\nUNITMAN_ERROR_KEY=opencode.workspace.project_clone_failed\n"
	if got := errorKeyFromOutput(output); got != "opencode.workspace.project_clone_failed" {
		t.Fatalf("unexpected key: %q", got)
	}
	if got := errorKeyFromOutput("no key here"); got != "" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestRedactURLHidesCredentials(t *testing.T) {
	input := "clone https://oauth2:TESTTOKENEXAMPLEVALUE@gitlab.example/services/api.git failed"
	got := redactURL(input)
	if strings.Contains(got, "TESTTOKENEXAMPLEVALUE") {
		t.Fatalf("token was not redacted: %q", got)
	}
	if !strings.Contains(got, "https://redacted@gitlab.example") {
		t.Fatalf("unexpected redaction: %q", got)
	}
}

func TestWorkspaceActivityInputJSONHasNoCredentials(t *testing.T) {
	command := ZapustitWorkspaceCommand{
		WorkspaceId: "ws-1",
		Branch:      "main",
		Projects: []Project{
			{
				ProjectId:   "project-1",
				ProjectName: "api",
				ProjectCode: "services/api",
				StorageUrl:  "https://gitlab.example/services/api.git",
				RepoId:      "repo-1",
				MainBranch:  "main",
			},
		},
	}

	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, pattern := range []string{"oauth2:", "token@", "TESTTOKENEXAMPLEVALUE", "Bearer", "ghp_", "github_pat_", "user:pass@"} {
		if strings.Contains(string(raw), pattern) {
			t.Fatalf("workspace input contains credential pattern %q: %s", pattern, raw)
		}
	}
	if !strings.Contains(string(raw), `"RepoId":"repo-1"`) {
		t.Fatalf("RepoId missing from workspace input: %s", raw)
	}
	if !strings.Contains(string(raw), `"StorageUrl":"https://gitlab.example/services/api.git"`) {
		t.Fatalf("StorageUrl must be clean: %s", raw)
	}
}
