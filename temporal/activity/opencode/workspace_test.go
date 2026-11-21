package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

func prepareWorkspaceDir(t *testing.T, root, workspaceID string) string {
	t.Helper()

	workspaceDir := filepath.Join(root, "workspaces", workspaceID)
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("cannot create workspace dir: %v", err)
	}

	return workspaceDir
}

func TestResolveWorkDirsWorkspace(t *testing.T) {
	root := t.TempDir()
	workspaceDir := prepareWorkspaceDir(t, root, "ws-1")

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

	if cwd != workspaceDir {
		t.Fatalf("cwd = %q, want %q", cwd, workspaceDir)
	}

	// Проекты живут в томе workspace_<id>_app внутри контейнера и на хосте
	// не видны: корни — контейнерные пути /workspace/<ProjectId>.
	wantRoots := []string{
		filepath.Join(workspaceContainerDir, "project-1"),
		filepath.Join(workspaceContainerDir, "project-2"),
	}
	if len(roots) != len(wantRoots) {
		t.Fatalf("roots = %+v, want %+v", roots, wantRoots)
	}
	for i, want := range wantRoots {
		if roots[i] != want {
			t.Fatalf("roots[%d] = %q, want %q", i, roots[i], want)
		}
	}
}

func TestResolveWorkDirsWorkspaceRejectsBadProjectID(t *testing.T) {
	root := t.TempDir()
	prepareWorkspaceDir(t, root, "ws-1")

	acts := &Activities{WorkDir: root}
	_, _, err := acts.resolveWorkDirs(HodCommand{
		WorkspaceID: "ws-1",
		WorkspaceProjects: []WorkspaceProject{
			{ProjectID: "project-1"},
			{ProjectID: "../escape"},
		},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid project id")
	}
}

func TestResolveWorkDirsWorkspaceRejectsTraversal(t *testing.T) {
	acts := &Activities{WorkDir: t.TempDir()}
	if _, _, err := acts.resolveWorkDirs(HodCommand{WorkspaceID: "../escape"}); err == nil {
		t.Fatal("expected an error for an invalid workspace id")
	}
}
