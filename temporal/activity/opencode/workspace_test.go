package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

func prepareWorkspaceDirs(t *testing.T, root, workspaceID string, projectIDs ...string) []string {
	t.Helper()

	workspaceDir := filepath.Join(root, "workspaces", workspaceID)
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("cannot create workspace dir: %v", err)
	}

	dirs := make([]string, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		dir := filepath.Join(workspaceDir, projectID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("cannot create project dir: %v", err)
		}
		dirs = append(dirs, dir)
	}

	return dirs
}

func TestResolveWorkDirsWorkspace(t *testing.T) {
	root := t.TempDir()
	dirs := prepareWorkspaceDirs(t, root, "ws-1", "project-1", "project-2")

	acts := &Activities{WorkDir: root}
	cwd, roots, err := acts.resolveWorkDirs(HodCommand{
		WorkspaceID: "ws-1",
		WorkspaceProjects: []WorkspaceProject{
			{ProjectID: "project-1", ProjectName: "api"},
			{ProjectID: "project-2", ProjectName: "web"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCwd := filepath.Join(root, "workspaces", "ws-1")
	if cwd != wantCwd {
		t.Fatalf("cwd = %q, want %q", cwd, wantCwd)
	}
	if len(roots) != 2 || roots[0] != dirs[0] || roots[1] != dirs[1] {
		t.Fatalf("unexpected roots: %+v", roots)
	}
}

func TestResolveWorkDirsWorkspaceMissingProject(t *testing.T) {
	root := t.TempDir()
	prepareWorkspaceDirs(t, root, "ws-1", "project-1")

	acts := &Activities{WorkDir: root}
	_, _, err := acts.resolveWorkDirs(HodCommand{
		WorkspaceID: "ws-1",
		WorkspaceProjects: []WorkspaceProject{
			{ProjectID: "project-1"},
			{ProjectID: "project-missing"},
		},
	})
	if err == nil {
		t.Fatal("expected an error for a missing project directory")
	}
}

func TestResolveWorkDirsWorkspaceRejectsTraversal(t *testing.T) {
	acts := &Activities{WorkDir: t.TempDir()}
	if _, _, err := acts.resolveWorkDirs(HodCommand{WorkspaceID: "../escape"}); err == nil {
		t.Fatal("expected an error for an invalid workspace id")
	}
}
