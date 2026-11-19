package utils

import (
	"context"
	"errors"
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
