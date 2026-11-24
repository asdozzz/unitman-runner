package monitor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testSecret = "test-shared-secret"

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSendHealthRequestPayloadAndSignature(t *testing.T) {
	type captured struct {
		method    string
		path      string
		mediaType string
		runnerID  string
		signature string
		payload   HealthPayload
		err       error
	}

	results := make(chan captured, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		result := captured{
			method:    r.Method,
			path:      r.URL.Path,
			mediaType: r.Header.Get("Content-Type"),
			runnerID:  r.Header.Get("X-Runner-Id"),
			signature: r.Header.Get("X-Runner-Signature"),
			err:       err,
		}
		if err == nil {
			_ = json.Unmarshal(body, &result.payload)
			if want := signBody(testSecret, body); result.signature != want {
				t.Errorf("signature mismatch: got %q want %q", result.signature, want)
			}
		}
		results <- result
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		Secret:     testSecret,
		RunnerID:   "runner-1",
		TaskQueue:  "runner-queue",
		HTTPClient: server.Client(),
	}
	payload := HealthPayload{
		RunnerID:    "runner-1",
		TaskQueue:   "runner-queue",
		Active:      true,
		DockerStats: `{"Name":"unit"}`,
		MemInfo:     "MemTotal: 1 kB",
		Unixtime:    12345,
	}

	if err := client.SendHealth(context.Background(), payload); err != nil {
		t.Fatalf("SendHealth: %v", err)
	}

	result := <-results
	if result.err != nil {
		t.Fatalf("read request body: %v", result.err)
	}
	if result.method != http.MethodPost {
		t.Errorf("method: got %q want POST", result.method)
	}
	if result.path != healthPath {
		t.Errorf("path: got %q want %q", result.path, healthPath)
	}
	if result.mediaType != "application/json" {
		t.Errorf("content-type: got %q", result.mediaType)
	}
	if result.signature == "" {
		t.Errorf("missing X-Runner-Signature header")
	}
	if result.runnerID != "runner-1" {
		t.Errorf("X-Runner-Id = %q, want runner-1", result.runnerID)
	}
	if result.payload != payload {
		t.Errorf("payload: got %+v want %+v", result.payload, payload)
	}
}

func TestSendNotificationsRequestPayloadAndSignature(t *testing.T) {
	type captured struct {
		path      string
		mediaType string
		runnerID  string
		signature string
		payload   NotificationsPayload
	}

	results := make(chan captured, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var payload NotificationsPayload
		_ = json.Unmarshal(body, &payload)
		results <- captured{
			path:      r.URL.Path,
			mediaType: r.Header.Get("Content-Type"),
			runnerID:  r.Header.Get("X-Runner-Id"),
			signature: r.Header.Get("X-Runner-Signature"),
			payload:   payload,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		Secret:     testSecret,
		RunnerID:   "runner-1",
		HTTPClient: server.Client(),
	}
	payload := NotificationsPayload{
		RunnerID: "runner-1",
		Notifications: []Notification{
			{
				UnitID:        "unit-1",
				ProjectID:     "project-1",
				UnitName:      "unit-one",
				ProjectName:   "project-one",
				ExpectedState: "running",
				ActualState:   "absent",
				EventType:     "mismatch",
				DetectedAt:    777,
			},
		},
	}

	if err := client.SendNotifications(context.Background(), payload); err != nil {
		t.Fatalf("SendNotifications: %v", err)
	}

	result := <-results
	if result.path != notificationsPath {
		t.Errorf("path: got %q want %q", result.path, notificationsPath)
	}
	if result.mediaType != "application/json" {
		t.Errorf("content-type: got %q", result.mediaType)
	}
	if result.signature == "" {
		t.Errorf("missing X-Runner-Signature header")
	}
	if result.runnerID != "runner-1" {
		t.Errorf("X-Runner-Id = %q, want runner-1", result.runnerID)
	}
	if len(result.payload.Notifications) != 1 {
		t.Fatalf("notifications: got %d want 1", len(result.payload.Notifications))
	}
	if result.payload.RunnerID != "runner-1" {
		t.Errorf("runnerId: got %q", result.payload.RunnerID)
	}
	if result.payload.Notifications[0] != payload.Notifications[0] {
		t.Errorf("notification: got %+v want %+v", result.payload.Notifications[0], payload.Notifications[0])
	}
}

func TestSendHealthAlwaysSendsRunnerIdentityHeaders(t *testing.T) {
	type captured struct {
		runnerID  string
		signature string
		body      []byte
	}
	results := make(chan captured, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		results <- captured{
			runnerID:  r.Header.Get("X-Runner-Id"),
			signature: r.Header.Get("X-Runner-Signature"),
			body:      body,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, RunnerID: "runner-1", Secret: testSecret, HTTPClient: server.Client()}
	if err := client.SendHealth(context.Background(), HealthPayload{RunnerID: "runner-1"}); err != nil {
		t.Fatalf("SendHealth: %v", err)
	}

	result := <-results
	if result.runnerID != "runner-1" {
		t.Fatalf("X-Runner-Id = %q", result.runnerID)
	}
	if want := signBody(testSecret, result.body); result.signature != want {
		t.Fatalf("signature = %q, want %q", result.signature, want)
	}
}

func TestSendHealthNonOKStatusReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if err := client.SendHealth(context.Background(), HealthPayload{RunnerID: "runner-1"}); err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
}
