package unit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCompose = `services:
  unit:
    image: test
  ide:
    image: test-ide
    read_only: true
    volumes:
      - unit_app:/home/coder/project:ro
    environment:
      - GIT_OPTIONAL_LOCKS=0
      - GIT_INDEX_FILE=/tmp/git-index
      - HOME=/home/coder
`

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

func TestApplyIdeEditorModeContentEdit(t *testing.T) {
	result := applyIdeEditorModeContent(testCompose, RezhimRedaktoraEdit)

	if !strings.Contains(result, "- unit_app:/home/coder/project:rw") {
		t.Fatalf("edit mode must mount project volume rw:\n%s", result)
	}
	if strings.Contains(result, "GIT_INDEX_FILE") {
		t.Fatalf("edit mode must use the real .git/index:\n%s", result)
	}
	if strings.Contains(result, ":ro") {
		t.Fatalf("edit mode must not keep ro project mount:\n%s", result)
	}
	if !strings.Contains(result, `user: "0:0"`) {
		t.Fatalf("edit mode must run ide as root to write the root-owned volume:\n%s", result)
	}
	if !strings.Contains(result, "- UNITMAN_IDE_EDIT_MODE=1") {
		t.Fatalf("edit mode must select editable ide settings:\n%s", result)
	}
}

func TestApplyIdeEditorModeContentView(t *testing.T) {
	// Сначала переводим файл в edit, затем обратно в view: обе операции
	// идемпотентны и не дублируют GIT_INDEX_FILE.
	edit := applyIdeEditorModeContent(testCompose, RezhimRedaktoraEdit)
	view := applyIdeEditorModeContent(edit, RezhimRedaktoraView)

	if !strings.Contains(view, "- unit_app:/home/coder/project:ro") {
		t.Fatalf("view mode must mount project volume ro:\n%s", view)
	}
	if strings.Count(view, "GIT_INDEX_FILE=/tmp/git-index") != 1 {
		t.Fatalf("view mode must have exactly one GIT_INDEX_FILE:\n%s", view)
	}
	if strings.Index(view, "GIT_INDEX_FILE") > strings.Index(view, "HOME=/home/coder") {
		t.Fatalf("GIT_INDEX_FILE must stay before HOME:\n%s", view)
	}
	if strings.Contains(view, `user: "0:0"`) {
		t.Fatalf("view mode must not run ide as root:\n%s", view)
	}
	if strings.Contains(view, "UNITMAN_IDE_EDIT_MODE") {
		t.Fatalf("view mode must not set edit settings flag:\n%s", view)
	}
}

func TestApplyIdeEditorModeWritesCompose(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(testCompose), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyIdeEditorMode(dir, RezhimRedaktoraEdit); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "/home/coder/project:rw") {
		t.Fatalf("compose.yaml was not switched to edit:\n%s", string(content))
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
	got := IdeRecreateCommand()
	want := []string{"docker", "compose", "--profile", "ide", "up", "-d", "--no-deps", "ide"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("unexpected recreate command: %#v", got)
	}
}
