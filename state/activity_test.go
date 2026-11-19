package state

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRecordActivityResultReadsNamesFromEnv проверяет совместимость со
// старыми payload: если ProjectName не передан, он дочитывается из .env юнита,
// а ContainerName собирается корректно (без пустого сегмента).
func TestRecordActivityResultReadsNamesFromEnv(t *testing.T) {
	dir := t.TempDir()

	unitDir := filepath.Join(dir, "projects", "p1", "units", "u1")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	env := "UNITMAN_UNIT_NAME=u\nUNITMAN_PROJECT_NAME=p\n"
	if err := os.WriteFile(filepath.Join(unitDir, ".env"), []byte(env), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Init(filepath.Join(dir, "state.db")); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		if err := Default().Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	RecordActivityResult("obnovlenie", "p1", "u1", "u", "", true)

	got, found, err := Default().Get("u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("Get: expected record to be created")
	}

	if got.ContainerName != "u.p" {
		t.Fatalf("ContainerName: expected %q, got %q", "u.p", got.ContainerName)
	}
	if got.ProjectName != "p" {
		t.Fatalf("ProjectName: expected %q, got %q", "p", got.ProjectName)
	}
	if got.Expected != ExpectedRunning {
		t.Fatalf("Expected: expected %q, got %q", ExpectedRunning, got.Expected)
	}
}
