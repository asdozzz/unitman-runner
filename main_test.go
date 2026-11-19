package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStartArgs(t *testing.T) {
	foreground, err := parseStartArgs(nil)
	if err != nil || foreground {
		t.Fatalf("empty args: foreground=%v err=%v", foreground, err)
	}

	foreground, err = parseStartArgs([]string{"--foreground"})
	if err != nil || !foreground {
		t.Fatalf("--foreground: foreground=%v err=%v", foreground, err)
	}

	if _, err := parseStartArgs([]string{"--unknown"}); err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}

func TestParseExecArgs(t *testing.T) {
	projectID, unitID, command, err := parseExecArgs([]string{"project-1", "unit-1", "echo", "hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if projectID != "project-1" || unitID != "unit-1" || command != "echo hello world" {
		t.Fatalf("unexpected result: %q %q %q", projectID, unitID, command)
	}

	projectID, unitID, command, err = parseExecArgs([]string{" project-1 ", " unit-1 ", "  pwd  "})
	if err != nil {
		t.Fatalf("unexpected error with spaces: %v", err)
	}
	if projectID != "project-1" || unitID != "unit-1" || command != "pwd" {
		t.Fatalf("unexpected trimmed result: %q %q %q", projectID, unitID, command)
	}

	if _, _, _, err := parseExecArgs([]string{"project-1", "unit-1"}); err == nil {
		t.Fatal("expected an error when command is missing")
	}

	if _, _, _, err := parseExecArgs([]string{"", "unit-1", "pwd"}); err == nil {
		t.Fatal("expected an error when ProjectId is empty")
	}

	if _, _, _, err := parseExecArgs([]string{"project-1", "", "pwd"}); err == nil {
		t.Fatal("expected an error when UnitId is empty")
	}
}

func TestWriteAndReadPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.pid")

	if err := writePID(path, 4242); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	pid, err := readPID(path)
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if pid != 4242 {
		t.Fatalf("pid = %d, want 4242", pid)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary pid file was not removed: %v", err)
	}
}

func TestReadPIDRejectsInvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.pid")
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := readPID(path); err == nil {
		t.Fatal("expected an error for invalid pid content")
	}
}

func TestTailFileReturnsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.log")
	var content strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 200)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 200 {
		t.Fatalf("got %d lines, want 200", len(lines))
	}
	if lines[0] != "line 100" || lines[len(lines)-1] != "line 299" {
		t.Fatalf("unexpected range: %q .. %q", lines[0], lines[len(lines)-1])
	}
}

func TestTailFileWithoutTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 2)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 2 || lines[0] != "two" || lines[1] != "three" {
		t.Fatalf("unexpected lines: %q", lines)
	}
}

func TestTailFileSmallFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.log")
	if err := os.WriteFile(path, []byte("one\ntwo\r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 200)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("unexpected lines: %q", lines)
	}
}

func TestTailFileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.log")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 200)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("got %d lines from an empty file", len(lines))
	}
}

func TestTailFileMultipleChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.log")
	var content strings.Builder
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&content, "line %05d: %s\n", i, strings.Repeat("x", 20))
	}
	if err := os.WriteFile(path, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 200)
	if err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if len(lines) != 200 {
		t.Fatalf("got %d lines, want 200", len(lines))
	}
	if !strings.HasPrefix(lines[0], "line 09800:") {
		t.Fatalf("unexpected first line: %q", lines[0])
	}
	if !strings.HasPrefix(lines[len(lines)-1], "line 09999:") {
		t.Fatalf("unexpected last line: %q", lines[len(lines)-1])
	}
}
