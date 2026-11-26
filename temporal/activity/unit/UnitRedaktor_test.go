package unit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRezhimRedaktora(t *testing.T) {
	cases := map[string]string{
		"":        RezhimRedaktoraView,
		"view":    RezhimRedaktoraView,
		"VIEW":    RezhimRedaktoraView,
		"edit":    RezhimRedaktoraEdit,
		" Edit ":  RezhimRedaktoraEdit,
		"locked":  RezhimRedaktoraView,
		"unknown": RezhimRedaktoraView,
	}

	for input, want := range cases {
		if got := NormalizeRezhimRedaktora(input); got != want {
			t.Fatalf("NormalizeRezhimRedaktora(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWriteIdeModeAndRead(t *testing.T) {
	dir := t.TempDir()

	if got := ReadIdeMode(dir); got != RezhimRedaktoraView {
		t.Fatalf("default ide mode = %q, want view", got)
	}

	if err := WriteIdeMode(dir, " EDIT "); err != nil {
		t.Fatalf("WriteIdeMode: %v", err)
	}
	if got := ReadIdeMode(dir); got != RezhimRedaktoraEdit {
		t.Fatalf("ide mode = %q, want edit", got)
	}

	if err := WriteIdeMode(dir, "unknown"); err != nil {
		t.Fatalf("WriteIdeMode: %v", err)
	}
	if got := ReadIdeMode(dir); got != RezhimRedaktoraView {
		t.Fatalf("ide mode = %q, want view", got)
	}
}

func TestWriteIdeModeRejectsEmptyPath(t *testing.T) {
	if err := WriteIdeMode("", RezhimRedaktoraView); err == nil {
		t.Fatal("expected an error for an empty unit path")
	}
}

func TestFindUnitDirectory(t *testing.T) {
	root := t.TempDir()
	unitDir := filepath.Join(root, "projects", "project-1", "units", "unit-1")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Посторонний проект без нужного юнита не должен мешать.
	if err := os.MkdirAll(filepath.Join(root, "projects", "project-2", "units", "unit-2"), 0755); err != nil {
		t.Fatal(err)
	}

	got, err := findUnitDirectory(root, "unit-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != unitDir {
		t.Fatalf("unexpected unit directory: %q, want %q", got, unitDir)
	}
}

func TestFindUnitDirectoryRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	for _, unitID := range []string{"", ".", "..", "../unit-1", "nested/unit-1"} {
		if _, err := findUnitDirectory(root, unitID); err == nil {
			t.Fatalf("expected an error for unit id %q", unitID)
		}
	}
}

func TestFindUnitDirectoryReportsMissingUnit(t *testing.T) {
	if _, err := findUnitDirectory(t.TempDir(), "unit-1"); err == nil {
		t.Fatal("expected an error for a missing unit")
	}
}

func TestIdeRecreateCommand(t *testing.T) {
	dir := t.TempDir()
	want := []string{
		"docker", "compose",
		"-f", "compose.yaml",
		"-f", "compose.ide-view.yaml",
		"--profile", "ide", "up", "-d", "--no-deps", "ide",
	}
	if got := IdeRecreateCommand(dir); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("unexpected recreate command: %#v", got)
	}

	if err := WriteIdeMode(dir, RezhimRedaktoraEdit); err != nil {
		t.Fatal(err)
	}
	want[5] = "compose.ide-edit.yaml"
	if got := IdeRecreateCommand(dir); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("unexpected edit recreate command: %#v", got)
	}
}
