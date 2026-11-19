package monitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDockerLogScript записывает каждый вызов docker в файл из FAKE_DOCKER_LOG.
const fakeDockerLogScript = `#!/bin/sh
echo "$@" >> "$FAKE_DOCKER_LOG"
exit 0
`

func TestRunCleanupPrunesImagesAndBuilderOnly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	installFakeDocker(t, fakeDockerLogScript)

	workdir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunCleanup(ctx, workdir)
	}()

	lines := waitForLogLines(t, logPath, 2, 5*time.Second)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunCleanup did not stop after context cancel")
	}

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 docker invocations, got %d: %v", len(lines), lines)
	}

	if lines[0] != "image prune -af" {
		t.Errorf("first command = %q, want %q", lines[0], "image prune -af")
	}
	if lines[1] != "builder prune -af" {
		t.Errorf("second command = %q, want %q", lines[1], "builder prune -af")
	}

	for _, line := range lines {
		if strings.Contains(line, "--volumes") {
			t.Errorf("cleanup must not prune volumes, got command %q", line)
		}
		if strings.Contains(line, "system prune") {
			t.Errorf("cleanup must not run system prune, got command %q", line)
		}
	}
}

// waitForLogLines ждёт появления не менее want непустых строк в файле лога.
func waitForLogLines(t *testing.T, path string, want int, timeout time.Duration) []string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lines []string

	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			lines = splitNonEmptyLines(string(data))
			if len(lines) >= want {
				return lines
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	return lines
}

func splitNonEmptyLines(value string) []string {
	raw := strings.Split(value, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines
}
