package monitor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunHealthSendsHealthPayload(t *testing.T) {
	payloads := make(chan HealthPayload, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != healthPath {
			t.Errorf("path = %q, want %q", r.URL.Path, healthPath)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}

		var payload HealthPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode body: %v", err)
		}

		select {
		case payloads <- payload:
		default:
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// docker stats может быть недоступен: health-push всё равно отправляется.
	installFakeDocker(t, "#!/bin/sh\nexit 0\n")

	client := &Client{
		BaseURL:    server.URL,
		RunnerID:   "runner-42",
		TaskQueue:  "runner-queue",
		HTTPClient: server.Client(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunHealth(ctx, nil, client, t.TempDir())
	}()

	var payload HealthPayload
	select {
	case payload = <-payloads:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("health request was not received within timeout")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHealth did not stop after context cancel")
	}

	if payload.RunnerID != "runner-42" {
		t.Errorf("runnerId = %q, want runner-42", payload.RunnerID)
	}
	if payload.TaskQueue != "runner-queue" {
		t.Errorf("taskQueue = %q, want runner-queue", payload.TaskQueue)
	}
	if !payload.Active {
		t.Error("active = false, want true")
	}
	if payload.Unixtime <= 0 {
		t.Errorf("unixtime = %d, want > 0", payload.Unixtime)
	}
}
