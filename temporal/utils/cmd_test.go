package utils

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecCommandSuccess(t *testing.T) {
	out, err := ExecCommand(t.TempDir(), []string{"sh", "-c", "printf hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out != "hello" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestExecCommandFailure(t *testing.T) {
	_, err := ExecCommand(t.TempDir(), []string{"sh", "-c", "printf boom >&2; exit 1"})
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error does not contain command output: %v", err)
	}
}

func TestExecCommandInteractiveInput(t *testing.T) {
	started := time.Now()

	_, err := ExecCommandWithTimeout(t.TempDir(), []string{"sh", "-c", "printf 'Password: '; sleep 10"}, 5*time.Second)
	if !errors.Is(err, ErrInteractiveInput) {
		t.Fatalf("expected ErrInteractiveInput, got: %v", err)
	}

	if time.Since(started) >= 5*time.Second {
		t.Fatalf("command was not interrupted by prompt detector")
	}
}

func TestExecCommandTimeout(t *testing.T) {
	started := time.Now()

	_, err := ExecCommandWithTimeout(t.TempDir(), []string{"sh", "-c", "sleep 10"}, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	if !strings.Contains(err.Error(), "не завершилась за") {
		t.Fatalf("unexpected error: %v", err)
	}

	if time.Since(started) >= 5*time.Second {
		t.Fatalf("command was not interrupted by timeout")
	}
}

func TestExecCommandStdinIsClosed(t *testing.T) {
	out, err := ExecCommand(t.TempDir(), []string{"sh", "-c", "read value; printf 'read-exit:%s' \"$?\""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "read-exit:1") {
		t.Fatalf("stdin should be closed, got output: %q", out)
	}
}

func TestExecCommandContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	_, err := ExecCommandContext(ctx, t.TempDir(), []string{"sh", "-c", "sleep 10"})
	if err == nil {
		t.Fatal("expected cancellation error")
	}

	if time.Since(started) >= 5*time.Second {
		t.Fatalf("command was not interrupted by context cancellation")
	}
}

func TestRedactURLCredentials(t *testing.T) {
	cases := map[string]string{
		"git clone https://oauth2:glpat-secret@gitlab.example.com/group/repo.git": "git clone https://redacted@gitlab.example.com/group/repo.git",
		"https://user@github.com/org/repo.git":                                    "https://redacted@github.com/org/repo.git",
		"https://gitlab.example.com/group/repo.git":                               "https://gitlab.example.com/group/repo.git",
		"no url here": "no url here",
	}

	for input, want := range cases {
		if got := RedactURLCredentials(input); got != want {
			t.Fatalf("RedactURLCredentials(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsComposeCommand(t *testing.T) {
	cases := []struct {
		want bool
		app  []string
	}{
		{true, []string{"docker", "compose", "up", "-d"}},
		{true, []string{"docker-compose", "up", "-d"}},
		{false, []string{"docker", "ps"}},
		{false, []string{"git", "status"}},
		{false, []string{"docker"}},
	}

	for _, testCase := range cases {
		if got := isComposeCommand(testCase.app); got != testCase.want {
			t.Fatalf("isComposeCommand(%v) = %v, want %v", testCase.app, got, testCase.want)
		}
	}
}

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\n\nUNITMAN_UNIT_NAME=ttt\nexport UNITMAN_PROJECT_NAME=\"unitman\"\nUNITMAN_MEMORY_LIMIT='5000m'\nBROKEN LINE\n=empty\nPATH=/usr/bin\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	overrides, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}

	want := map[string]string{
		"UNITMAN_UNIT_NAME":    "ttt",
		"UNITMAN_PROJECT_NAME": "unitman",
		"UNITMAN_MEMORY_LIMIT": "5000m",
		"PATH":                 "/usr/bin",
	}

	if len(overrides) != len(want) {
		t.Fatalf("parseEnvFile returned %d entries, want %d: %v", len(overrides), len(want), overrides)
	}

	for key, value := range want {
		if overrides[key] != value {
			t.Fatalf("parseEnvFile[%q] = %q, want %q", key, overrides[key], value)
		}
	}
}

func TestEnvWithOverrides(t *testing.T) {
	base := []string{"PATH=/usr/bin", "UNITMAN_UNIT_NAME=ppp", "HOME=/home/leo"}
	merged := envWithOverrides(base, map[string]string{"UNITMAN_UNIT_NAME": "ttt", "COMPOSE_PROJECT_NAME": "ttt_unitman"})

	joined := strings.Join(merged, "\n")

	if strings.Contains(joined, "UNITMAN_UNIT_NAME=ppp") {
		t.Fatalf("stale value must be replaced: %s", joined)
	}

	if !strings.Contains(joined, "UNITMAN_UNIT_NAME=ttt") ||
		!strings.Contains(joined, "COMPOSE_PROJECT_NAME=ttt_unitman") ||
		!strings.Contains(joined, "PATH=/usr/bin") ||
		!strings.Contains(joined, "HOME=/home/leo") {
		t.Fatalf("unexpected merged env: %s", joined)
	}
}

func TestApplyEnvFileForComposeOverridesStaleProcessEnv(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("UNITMAN_UNIT_NAME=ttt\nUNITMAN_PROJECT_NAME=unitman\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("UNITMAN_UNIT_NAME", "ppp")
	t.Setenv("UNITMAN_PROJECT_NAME", "exunit-php")

	cmd := &exec.Cmd{}
	applyEnvFileForCompose(cmd, dir, []string{"docker", "compose", "up", "-d", "--build"})

	if cmd.Env == nil {
		t.Fatal("env must be applied for compose commands")
	}

	values := map[string]int{}
	for _, entry := range cmd.Env {
		key, value, _ := strings.Cut(entry, "=")
		if key == "UNITMAN_UNIT_NAME" || key == "UNITMAN_PROJECT_NAME" {
			values[key+"="+value]++
		}
	}

	if len(values) != 2 {
		t.Fatalf("stale env must be fully replaced, got: %v", values)
	}

	if values["UNITMAN_UNIT_NAME=ttt"] != 1 || values["UNITMAN_PROJECT_NAME=unitman"] != 1 {
		t.Fatalf("env file values must win, got: %v", values)
	}
}

func TestApplyEnvFileForComposeSkipsNonCompose(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("UNITMAN_UNIT_NAME=ttt\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cmd := &exec.Cmd{}
	applyEnvFileForCompose(cmd, dir, []string{"sh", "-c", "true"})

	if cmd.Env != nil {
		t.Fatalf("env must not be applied for non-compose commands, got: %v", cmd.Env)
	}
}

func TestApplyEnvFileForComposeWithoutEnvFile(t *testing.T) {
	cmd := &exec.Cmd{}
	applyEnvFileForCompose(cmd, t.TempDir(), []string{"docker", "compose", "ps"})

	if cmd.Env != nil {
		t.Fatalf("env must stay inherited when .env is missing, got: %v", cmd.Env)
	}
}
