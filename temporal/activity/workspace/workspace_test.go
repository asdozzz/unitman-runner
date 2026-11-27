package workspace

import (
	"context"
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

// Guard входных payload'ов активностей объединён в единый table-driven тест
// TestActivityInputsJSONHasNoCredentials (temporal/utils/payload_guard_test.go).

// withExecWorkspace подменяет исполнение команд workspace и восстанавливает
// исходную реализацию после теста. Возвращает срез полученных команд.
func withExecWorkspace(t *testing.T) *[]string {
	t.Helper()

	commands := make([]string, 0, 8)
	previous := execWorkspace
	execWorkspace = func(_ context.Context, _, command, _ string, _ []string) (string, error) {
		commands = append(commands, command)
		return "", nil
	}

	previousCopy := copyFileToWorkspace
	copyFileToWorkspace = func(_ context.Context, _, _, target string) (string, error) {
		commands = append(commands, "cp -> "+target)
		return "", nil
	}

	t.Cleanup(func() {
		execWorkspace = previous
		copyFileToWorkspace = previousCopy
	})

	return &commands
}

// withDownloadWorkspaceFile подменяет скачивание файла на выдачу temp-файла
// с заданным содержимым. Возвращает счётчик вызовов.
func withDownloadWorkspaceFile(t *testing.T, content string) *int {
	t.Helper()

	calls := 0
	previous := downloadWorkspaceFile
	downloadWorkspaceFile = func(_ context.Context, _ string) (string, error) {
		calls++
		file, err := os.CreateTemp(t.TempDir(), "workspace_file_*")
		if err != nil {
			return "", err
		}
		if _, err := file.WriteString(content); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		return file.Name(), nil
	}
	t.Cleanup(func() { downloadWorkspaceFile = previous })

	return &calls
}

func TestValidWorkspaceFileNameRejectsUnsafeNames(t *testing.T) {
	rejected := []string{
		"",
		"   ",
		".",
		"..",
		"../secret.txt",
		"dir/file.txt",
		`dir\file.txt`,
		".hidden",
		"with\nnewline",
		"with\x00nul",
		workspaceCodeWorkspaceName,
		workspaceFilesDirName,
	}
	for _, name := range rejected {
		if validWorkspaceFileName(name) {
			t.Fatalf("expected %q to be rejected", name)
		}
	}

	for _, name := range []string{"notes.txt", "image.png", "a-b_c.md"} {
		if !validWorkspaceFileName(name) {
			t.Fatalf("expected %q to be accepted", name)
		}
	}
}

func TestApplyWorkspaceFilesCopiesAndMarksReadOnly(t *testing.T) {
	commands := withExecWorkspace(t)
	downloads := withDownloadWorkspaceFile(t, "hello")
	tracker := newStepTracker("wf-1")

	err := applyWorkspaceFiles(
		context.Background(),
		t.TempDir(),
		[]WorkspaceFile{{FileId: "file-1", Name: "notes.txt", Url: "https://example.com/file"}},
		tracker,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *downloads != 1 {
		t.Fatalf("expected one download, got %d", *downloads)
	}

	joined := strings.Join(*commands, "\n")
	if !strings.Contains(joined, "mkdir -p") || !strings.Contains(joined, workspaceFilesDirName) {
		t.Fatalf("files directory was not prepared:\n%s", joined)
	}
	if !strings.Contains(joined, "chmod 0444") || !strings.Contains(joined, "/unitman_files/notes.txt") {
		t.Fatalf("file was not made read-only:\n%s", joined)
	}
}

func TestApplyWorkspaceFilesRejectsInvalidNameBeforeDownload(t *testing.T) {
	withExecWorkspace(t)
	downloads := withDownloadWorkspaceFile(t, "malicious")
	tracker := newStepTracker("wf-1")

	err := applyWorkspaceFiles(
		context.Background(),
		t.TempDir(),
		[]WorkspaceFile{{FileId: "file-1", Name: "../escape.txt", Url: "https://example.com/file"}},
		tracker,
	)
	if err == nil {
		t.Fatal("expected error for invalid file name")
	}
	if *downloads != 0 {
		t.Fatalf("invalid name must be rejected before download, got %d downloads", *downloads)
	}
}

func TestRemoveWorkspaceFilesRunsRmAndRejectsTraversal(t *testing.T) {
	commands := withExecWorkspace(t)
	tracker := newStepTracker("wf-1")

	if err := removeWorkspaceFiles(context.Background(), t.TempDir(), []string{"old.txt"}, tracker); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if joined := strings.Join(*commands, "\n"); !strings.Contains(joined, "rm -f") || !strings.Contains(joined, "/unitman_files/old.txt") {
		t.Fatalf("rm command was not issued:\n%s", joined)
	}

	if err := removeWorkspaceFiles(context.Background(), t.TempDir(), []string{"../outside.txt"}, tracker); err == nil {
		t.Fatal("expected traversal name to be rejected")
	}
}

func TestGenerateCodeWorkspaceIncludesFilesFolder(t *testing.T) {
	projects := []Project{{ProjectId: "project-1", ProjectName: "api"}}

	for _, includeFiles := range []bool{true, false} {
		commands := withExecWorkspace(t)
		tracker := newStepTracker("wf-1")

		if err := generateCodeWorkspace(context.Background(), t.TempDir(), projects, includeFiles, tracker); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		joined := strings.Join(*commands, "\n")
		if !strings.Contains(joined, "workspace.code-workspace") {
			t.Fatalf("code-workspace command is missing:\n%s", joined)
		}
		if !strings.Contains(joined, "/workspace/project-1") {
			t.Fatalf("project folder is missing:\n%s", joined)
		}

		hasFiles := strings.Contains(joined, workspaceFilesDirName)
		if hasFiles != includeFiles {
			t.Fatalf("includeFiles=%v but files folder presence=%v:\n%s", includeFiles, hasFiles, joined)
		}
	}
}

func TestExecCommandInWorkspaceDelegatesToExecWorkspace(t *testing.T) {
	type captured struct {
		dir     string
		command string
		workDir string
		env     []string
	}
	var got captured

	previous := execWorkspace
	execWorkspace = func(_ context.Context, dir, command, workDir string, env []string) (string, error) {
		got = captured{dir: dir, command: command, workDir: workDir, env: env}
		return "ok", nil
	}
	t.Cleanup(func() { execWorkspace = previous })

	output, err := ExecCommandInWorkspace(context.Background(), "/host/ws", "echo hi", "/workspace", []string{"A=1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "ok" {
		t.Fatalf("unexpected output: %q", output)
	}
	if got.dir != "/host/ws" || got.command != "echo hi" || got.workDir != "/workspace" {
		t.Fatalf("unexpected delegation args: %+v", got)
	}
	if len(got.env) != 1 || got.env[0] != "A=1" {
		t.Fatalf("unexpected env: %+v", got.env)
	}
}

func TestObnovitWorkspaceActivityRejectsInvalidID(t *testing.T) {
	result, err := ObnovitWorkspaceActivity(context.Background(), ObnovitWorkspaceCommand{
		WorkspaceId: "../escape",
	})
	if err != nil {
		t.Fatalf("activity must not return transport error: %v", err)
	}
	if result.Success != 0 {
		t.Fatalf("invalid workspace id must fail, got success=%d", result.Success)
	}
	if result.Files == nil || result.Projects == nil {
		t.Fatalf("result slices must be initialized: %+v", result)
	}
}

func TestObnovitWorkspaceActivityFailsWhenDirectoryMissing(t *testing.T) {
	result, err := ObnovitWorkspaceActivity(context.Background(), ObnovitWorkspaceCommand{
		WorkspaceId: "workspace-does-not-exist-987654321",
		NewProjects: []Project{{ProjectId: "project-1", ProjectName: "api"}},
	})
	if err != nil {
		t.Fatalf("activity must not return transport error: %v", err)
	}
	if result.Success != 0 {
		t.Fatalf("missing workspace directory must fail, got success=%d", result.Success)
	}
	if len(result.Projects) != 0 {
		t.Fatalf("no project may be processed when directory is missing: %+v", result.Projects)
	}
}
