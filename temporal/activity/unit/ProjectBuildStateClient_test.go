package unit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const buildStateTestSecret = "test-runner-secret"

func TestProjectBuildStateClientGetSignsEmptyBody(t *testing.T) {
	var (
		gotPath      string
		gotRunnerID  string
		gotSignature string
		gotBody      string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		gotPath = r.URL.Path
		gotRunnerID = r.Header.Get(ProjectBuildStateRunnerIDHeader)
		gotSignature = r.Header.Get(ProjectBuildStateSignatureHeader)
		gotBody = string(body)

		if gotSignature != signProjectBuildState(body, buildStateTestSecret) {
			t.Errorf("unexpected signature for GET")
		}

		_ = json.NewEncoder(w).Encode(ProjectBuildState{Built: false})
	}))
	defer server.Close()

	client := NewProjectBuildStateClient(server.URL, "runner-1", buildStateTestSecret, server.Client())
	state, err := client.GetBuildState(context.Background(), "project-1")
	if err != nil {
		t.Fatalf("GetBuildState: %v", err)
	}
	if state.Built {
		t.Fatal("expected built=false")
	}
	if gotPath != "/api/runner/runner-1/projects/project-1/build-state" {
		t.Fatalf("unexpected path: %q", gotPath)
	}
	if gotBody != "" {
		t.Fatalf("GET must sign an empty body, got %q", gotBody)
	}
	if gotSignature == "" {
		t.Fatal("missing X-Runner-Signature header")
	}
	if gotRunnerID != "runner-1" {
		t.Fatalf("X-Runner-Id = %q, want runner-1", gotRunnerID)
	}
}

func TestProjectBuildStateClientRecordSendsSignedBody(t *testing.T) {
	builtAt := int64(1764000000)
	commit := "a1b2c3d4"
	branch := "main"

	var (
		gotMethod    string
		gotRecord    ProjectBuildRecord
		gotRunnerID  string
		gotSignature string
		rawBody      string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		gotMethod = r.Method
		gotRunnerID = r.Header.Get(ProjectBuildStateRunnerIDHeader)
		gotSignature = r.Header.Get(ProjectBuildStateSignatureHeader)
		rawBody = string(body)

		if gotSignature != signProjectBuildState(body, buildStateTestSecret) {
			t.Errorf("unexpected signature for PUT")
		}

		if err := json.Unmarshal(body, &gotRecord); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		_ = json.NewEncoder(w).Encode(ProjectBuildState{
			Built:   true,
			BuiltAt: &builtAt,
			Commit:  &commit,
			Branch:  &branch,
		})
	}))
	defer server.Close()

	client := NewProjectBuildStateClient(server.URL, "runner-1", buildStateTestSecret, server.Client())
	record := ProjectBuildRecord{BuiltAt: &builtAt, Commit: &commit, Branch: &branch}
	state, err := client.RecordBuildState(context.Background(), "project-1", record)
	if err != nil {
		t.Fatalf("RecordBuildState: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("method = %q, want PUT", gotMethod)
	}
	if gotRecord.BuiltAt == nil || *gotRecord.BuiltAt != builtAt {
		t.Fatalf("unexpected builtAt: %#v", gotRecord.BuiltAt)
	}
	if gotRecord.Commit == nil || *gotRecord.Commit != commit {
		t.Fatalf("unexpected commit: %#v", gotRecord.Commit)
	}
	if gotRecord.Branch == nil || *gotRecord.Branch != branch {
		t.Fatalf("unexpected branch: %#v", gotRecord.Branch)
	}
	if gotSignature == "" || rawBody == "" {
		t.Fatal("PUT must carry a signed non-empty body")
	}
	if gotRunnerID != "runner-1" {
		t.Fatalf("X-Runner-Id = %q, want runner-1", gotRunnerID)
	}
	if !state.Built {
		t.Fatal("expected built=true in response")
	}
}

func TestProjectBuildStateClientTypedErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		code       string
		assertFunc func(*testing.T, *ProjectBuildStateAPIError)
	}{
		{
			name:   "invalid project id",
			status: http.StatusBadRequest,
			code:   ProjectBuildStateInvalidProjectIDCode,
			assertFunc: func(t *testing.T, apiErr *ProjectBuildStateAPIError) {
				if apiErr.Code != ProjectBuildStateInvalidProjectIDCode {
					t.Fatalf("code = %q", apiErr.Code)
				}
			},
		},
		{
			name:   "invalid payload",
			status: http.StatusBadRequest,
			code:   ProjectBuildStateInvalidPayloadCode,
			assertFunc: func(t *testing.T, apiErr *ProjectBuildStateAPIError) {
				if apiErr.Code != ProjectBuildStateInvalidPayloadCode {
					t.Fatalf("code = %q", apiErr.Code)
				}
			},
		},
		{
			name:   "invalid signature",
			status: http.StatusUnauthorized,
			code:   RunnerInvalidSignatureCode,
			assertFunc: func(t *testing.T, apiErr *ProjectBuildStateAPIError) {
				if !apiErr.IsInvalidSignature() {
					t.Fatal("expected IsInvalidSignature")
				}
			},
		},
		{
			name:   "runner not found",
			status: http.StatusNotFound,
			code:   RunnerNotFoundCode,
			assertFunc: func(t *testing.T, apiErr *ProjectBuildStateAPIError) {
				if !apiErr.IsRunnerNotFound() {
					t.Fatal("expected IsRunnerNotFound")
				}
			},
		},
		{
			name:   "runner inactive",
			status: http.StatusConflict,
			code:   RunnerInactiveCode,
			assertFunc: func(t *testing.T, apiErr *ProjectBuildStateAPIError) {
				if !apiErr.IsRunnerInactive() {
					t.Fatal("expected IsRunnerInactive")
				}
			},
		},
		{
			name:   "auth disabled",
			status: http.StatusServiceUnavailable,
			code:   RunnerAuthDisabledCode,
			assertFunc: func(t *testing.T, apiErr *ProjectBuildStateAPIError) {
				if !apiErr.IsAuthDisabled() {
					t.Fatal("expected IsAuthDisabled")
				}
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "fail",
					"data": map[string]string{
						"code":    testCase.code,
						"message": testCase.code,
					},
				})
			}))
			defer server.Close()

			client := NewProjectBuildStateClient(server.URL, "runner-1", buildStateTestSecret, server.Client())
			_, err := client.GetBuildState(context.Background(), "project-1")
			if err == nil {
				t.Fatal("expected an error")
			}

			apiErr, ok := AsProjectBuildStateAPIError(err)
			if !ok {
				t.Fatalf("expected *ProjectBuildStateAPIError, got %T: %v", err, err)
			}
			if apiErr.StatusCode != testCase.status {
				t.Fatalf("status = %d, want %d", apiErr.StatusCode, testCase.status)
			}
			if apiErr.Code != testCase.code {
				t.Fatalf("code = %q, want %q", apiErr.Code, testCase.code)
			}
			testCase.assertFunc(t, apiErr)
		})
	}
}

func TestProjectBuildStateClientRejectsMissingSecret(t *testing.T) {
	client := NewProjectBuildStateClient("http://backend", "runner-1", "", &http.Client{Timeout: time.Second})

	_, err := client.GetBuildState(context.Background(), "project-1")
	if err == nil {
		t.Fatal("expected an error when the secret is empty")
	}
	if !strings.Contains(err.Error(), "RunnerSecret") {
		t.Fatalf("error should name RunnerSecret: %v", err)
	}
}

func TestProjectBuildStateClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"built":false}`))
	}))
	defer server.Close()

	client := NewProjectBuildStateClient(server.URL, "runner-1", buildStateTestSecret, &http.Client{Timeout: 10 * time.Millisecond})

	_, err := client.GetBuildState(context.Background(), "project-1")
	if err == nil {
		t.Fatal("expected a timeout error")
	}

	var apiErr *ProjectBuildStateAPIError
	if errors.As(err, &apiErr) {
		t.Fatalf("timeout must not be parsed as backend API error: %v", err)
	}
}
