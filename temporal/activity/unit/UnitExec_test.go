package unit

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUnitContainerCommand(t *testing.T) {
	want := []string{"docker", "compose", "exec", "-T", "unit", "sh", "-c", "echo hello"}
	if got := UnitContainerCommand("echo hello"); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected argv: %#v", got)
	}
}

func TestUnitContainerCommandWithOptions(t *testing.T) {
	want := []string{
		"docker", "compose", "exec", "-T",
		"--workdir", "/app",
		"-e", "A=1",
		"-e", "B=2",
		"unit", "sh", "-c", "echo hello",
	}
	got := UnitContainerCommandWithOptions("echo hello", UnitExecOptions{
		WorkDir: "/app",
		Env:     []string{"A=1", "", "B=2"},
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected argv: %#v", got)
	}
}

func TestUnitContainerCommandWithCustomService(t *testing.T) {
	want := []string{
		"docker", "compose", "exec", "-T",
		"--workdir", "/workspace",
		"workspace", "sh", "-c", "echo hello",
	}
	got := UnitContainerCommandWithOptions("echo hello", UnitExecOptions{
		WorkDir: "/workspace",
		Service: "workspace",
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected argv: %#v", got)
	}
}

func TestExecInUnitContainerPassesDirectoryAndCommand(t *testing.T) {
	var (
		gotDir  string
		gotArgs []string
	)

	exec := func(dir string, args []string) (string, error) {
		gotDir = dir
		gotArgs = args
		return "ok", nil
	}

	out, err := execInUnitContainer("/tmp/unit", "printf hello", exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected output: %q", out)
	}
	if gotDir != "/tmp/unit" {
		t.Fatalf("unexpected directory: %q", gotDir)
	}
	if want := UnitContainerCommand("printf hello"); !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("unexpected argv: %#v", gotArgs)
	}
}

func TestExecInUnitContainerRejectsEmptyCommand(t *testing.T) {
	called := false
	exec := func(string, []string) (string, error) {
		called = true
		return "", nil
	}

	if _, err := execInUnitContainer("/tmp/unit", "   ", exec); err == nil {
		t.Fatal("expected an error for an empty command")
	}
	if called {
		t.Fatal("executor must not be called for an empty command")
	}
}

func TestExecInUnitContainerRejectsEmptyDirectory(t *testing.T) {
	if _, err := execInUnitContainer("", "pwd", func(string, []string) (string, error) {
		return "", nil
	}); err == nil {
		t.Fatal("expected an error for an empty unit directory")
	}
}

func TestUnitDirectoryBuildsRunnerLayout(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "projects", "project-1", "units", "unit-1")
	if err := os.MkdirAll(want, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := unitDirectory(root, "project-1", "unit-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("unexpected unit directory: %q, want %q", got, want)
	}
}

func TestUnitDirectoryReportsMissingUnit(t *testing.T) {
	_, err := unitDirectory(t.TempDir(), "project-1", "unit-1")
	if err == nil {
		t.Fatal("expected an error for a missing unit directory")
	}
	if !strings.Contains(err.Error(), "каталог юнита не найден") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnitDirectoryRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		projectID string
		unitID    string
	}{
		{"", "unit-1"},
		{"..", "unit-1"},
		{"../escape", "unit-1"},
		{"project-1", ""},
		{"project-1", ".."},
		{"project-1", "../escape"},
		{"project-1", "nested/unit"},
	}

	for _, c := range cases {
		if _, err := unitDirectory(root, c.projectID, c.unitID); err == nil {
			t.Fatalf("expected an error for project=%q unit=%q", c.projectID, c.unitID)
		}
	}
}

func TestIsUnitPathSegment(t *testing.T) {
	for _, segment := range []string{"unit-1", "01a1d8f0-3f43-72ee-81eb-0c7d214b5b49", "a"} {
		if !isUnitPathSegment(segment) {
			t.Fatalf("%q must be a valid path segment", segment)
		}
	}

	for _, segment := range []string{"", ".", "..", "../unit", "nested/unit", `nested\unit`} {
		if isUnitPathSegment(segment) {
			t.Fatalf("%q must be rejected", segment)
		}
	}
}

func TestRunUnitCommandDoesNotExecuteWithoutUnitDirectory(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	_, err = RunUnitCommand("project-1", "unit-1", "pwd")
	if err == nil {
		t.Fatal("expected an error for a missing unit directory")
	}
	if errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "каталог юнита не найден") {
		t.Fatalf("unexpected error: %v", err)
	}
}
