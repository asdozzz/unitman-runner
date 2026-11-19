package unit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitVolumesUsesStableUnitID(t *testing.T) {
	volumes := UnitVolumes("unit-123")

	if volumes.App != "unitman_unit_app_unit-123" {
		t.Fatalf("unexpected app volume: %q", volumes.App)
	}
	if volumes.Docker != "unitman_unit_docker_unit-123" {
		t.Fatalf("unexpected docker volume: %q", volumes.Docker)
	}
	if volumes.Containers != "unitman_unit_containers_unit-123" {
		t.Fatalf("unexpected containers volume: %q", volumes.Containers)
	}
	if len(volumes.Names()) != 3 {
		t.Fatalf("expected three volume names, got %#v", volumes.Names())
	}
}

func TestSelectUnitComposeFile(t *testing.T) {
	if got := SelectUnitComposeFile("asdozzz/unitman-unit:latest"); got != unitPodmanComposeFile {
		t.Fatalf("unexpected podman compose file: %q", got)
	}
	if got := SelectUnitComposeFile("asdozzz/dind-unit:latest"); got != unitDindComposeFile {
		t.Fatalf("unexpected dind compose file: %q", got)
	}
}

func TestWriteUnitEnvFilePreservesOrder(t *testing.T) {
	unitPath := t.TempDir()

	err := WriteUnitEnvFile(unitPath, [][2]string{
		{"UNITMAN_UNIT_ID", "unit-1"},
		{"UNITMAN_BRANCH_NAME", "feature/test"},
	})
	if err != nil {
		t.Fatalf("WriteUnitEnvFile: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(unitPath, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}

	want := "UNITMAN_UNIT_ID=unit-1\nUNITMAN_BRANCH_NAME=feature/test\n"
	if string(content) != want {
		t.Fatalf("unexpected .env content: %q", string(content))
	}
}

func TestWriteUnitEnvFileRejectsInvalidKey(t *testing.T) {
	err := WriteUnitEnvFile(t.TempDir(), [][2]string{{"BAD=KEY", "value"}})
	if err == nil {
		t.Fatal("expected an error for an invalid env key")
	}
}

func TestReadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\nUNITMAN_UNIT_ID=unit-1\nUNITMAN_BRANCH_NAME=feature/test\nEMPTY=\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	values, err := ReadEnvFile(path)
	if err != nil {
		t.Fatalf("ReadEnvFile: %v", err)
	}

	if values["UNITMAN_UNIT_ID"] != "unit-1" {
		t.Fatalf("unexpected unit id: %q", values["UNITMAN_UNIT_ID"])
	}
	if values["UNITMAN_BRANCH_NAME"] != "feature/test" {
		t.Fatalf("unexpected branch: %q", values["UNITMAN_BRANCH_NAME"])
	}
	if values["EMPTY"] != "" {
		t.Fatalf("unexpected empty value: %q", values["EMPTY"])
	}
}

func TestRedactStorageURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"https token", "https://token@github.com/org/repo.git", "https://redacted@github.com/org/repo.git"},
		{"scp like", "git@github.com:org/repo.git", "<redacted>"},
		{"no credentials", "https://github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"empty", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactStorageURL(c.raw)
			if got != c.want {
				t.Fatalf("RedactStorageURL(%q) = %q, want %q", c.raw, got, c.want)
			}
			if strings.Contains(got, "token") {
				t.Fatalf("credentials leaked: %q", got)
			}
		})
	}
}
