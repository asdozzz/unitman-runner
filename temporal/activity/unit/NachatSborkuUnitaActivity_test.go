package unit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"runner/config"
)

const buildStateTestBuiltAt = int64(1764000000)

// fakeBuildStateBackend повторяет поведение ProjectBuildStateController:
// проверяет HMAC, учитывает GET/PUT и хранит состояние проекта.
type fakeBuildStateBackend struct {
	mu         sync.Mutex
	secret     string
	built      bool
	builtAt    int64
	commit     string
	branch     string
	delay      time.Duration
	failCode   string
	failStatus int

	gets         int
	puts         int
	authFailures int
	lastRecord   ProjectBuildRecord
	paths        []string
}

func (b *fakeBuildStateBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.delay > 0 {
		time.Sleep(b.delay)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.paths = append(b.paths, r.URL.Path)

	if r.Header.Get(ProjectBuildStateSignatureHeader) != signProjectBuildState(body, b.secret) {
		b.authFailures++
		b.writeFail(w, http.StatusUnauthorized, RunnerInvalidSignatureCode)
		return
	}

	if b.failStatus != 0 {
		b.writeFail(w, b.failStatus, b.failCode)
		return
	}

	switch r.Method {
	case http.MethodGet:
		b.gets++
		b.writeState(w)
	case http.MethodPut:
		b.puts++
		var record ProjectBuildRecord
		if err := json.Unmarshal(body, &record); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b.lastRecord = record
		b.built = true
		if record.BuiltAt != nil {
			b.builtAt = *record.BuiltAt
		}
		if record.Commit != nil {
			b.commit = *record.Commit
		}
		if record.Branch != nil {
			b.branch = *record.Branch
		}
		b.writeState(w)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (b *fakeBuildStateBackend) writeState(w http.ResponseWriter) {
	state := ProjectBuildState{Built: b.built}
	if b.built {
		builtAt := b.builtAt
		commit := b.commit
		branch := b.branch
		state.BuiltAt = &builtAt
		state.Commit = &commit
		state.Branch = &branch
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (b *fakeBuildStateBackend) writeFail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "fail",
		"data": map[string]string{
			"code":    code,
			"message": code,
		},
	})
}

func (b *fakeBuildStateBackend) snapshot() (gets, puts, authFailures int, last ProjectBuildRecord, paths []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	paths = append([]string(nil), b.paths...)
	return b.gets, b.puts, b.authFailures, b.lastRecord, paths
}

// activityTestSpy фиксирует вызовы git/клонирования.
type activityTestSpy struct {
	cloneCalls      int
	cloneProjectDir string
	cloneStorageURL string
	commit          string
	branch          string
	execCalls       []string
}

func newSborkaTestDeps(t *testing.T, baseURL, secret string, spy *activityTestSpy, httpClient *http.Client) sborkaDependencies {
	t.Helper()

	workdir := t.TempDir()

	return sborkaDependencies{
		blocked: func() (bool, string) { return false, "" },
		parseConfig: func() (config.Configuration, error) {
			return config.Configuration{ApiHost: baseURL, RunnerID: "runner-config", RunnerSecret: secret}, nil
		},
		getenv: func(string) string {
			return ""
		},
		getwd:    func() (string, error) { return workdir, nil },
		mkdirAll: os.MkdirAll,
		execCommand: func(dir string, args []string) (string, error) {
			spy.execCalls = append(spy.execCalls, strings.Join(args, " "))

			if len(args) >= 3 && args[0] == "git" && args[1] == "remote" && args[2] == "get-url" {
				return "https://git.example.com/org/repo.git", nil
			}
			if len(args) >= 2 && args[0] == "sh" {
				return "unitman.yaml: ok", nil
			}

			return "", nil
		},
		now: func() time.Time { return time.Unix(buildStateTestBuiltAt, 0).UTC() },
		httpClient: func() *http.Client {
			if httpClient != nil {
				return httpClient
			}
			return &http.Client{Timeout: time.Second}
		},
		ensureProjectRepo: func(_ context.Context, projectDir, storageURL string) (string, string, error) {
			spy.cloneCalls++
			spy.cloneProjectDir = projectDir
			spy.cloneStorageURL = storageURL
			if err := os.MkdirAll(projectDir, os.ModePerm); err != nil {
				return "", "", err
			}
			return spy.commit, spy.branch, nil
		},
		newBuildStateClient: NewProjectBuildStateClient,
	}
}

func testSborkaCommand(runnerID string) NachatSborkuUnita {
	return NachatSborkuUnita{
		ProjectId:  "project-1",
		Id:         "unit-1",
		Name:       "unit",
		Branch:     "feature/x",
		StorageUrl: "https://token@git.example.com/org/repo.git",
		RunnerId:   runnerID,
	}
}

func TestNachatSborkuUnitaBuildsProjectLazily(t *testing.T) {
	backend := &fakeBuildStateBackend{secret: buildStateTestSecret}
	server := httptest.NewServer(backend)
	defer server.Close()

	spy := &activityTestSpy{commit: "abc123", branch: "main"}
	deps := newSborkaTestDeps(t, server.URL, buildStateTestSecret, spy, nil)

	result, err := nachatSborkuUnitaActivity(context.Background(), testSborkaCommand("runner-cmd"), deps)
	if err != nil {
		t.Fatalf("activity error: %v", err)
	}
	if result.Success != 1 {
		t.Fatalf("expected success, steps: %#v", result.Steps)
	}
	if result.Config != "unitman.yaml: ok" {
		t.Fatalf("unexpected config: %q", result.Config)
	}

	gets, puts, authFailures, lastRecord, paths := backend.snapshot()
	if authFailures != 0 {
		t.Fatalf("signature failures: %d", authFailures)
	}
	if gets != 1 {
		t.Fatalf("GET count = %d, want 1", gets)
	}
	if puts != 1 {
		t.Fatalf("PUT count = %d, want 1", puts)
	}
	if spy.cloneCalls != 1 {
		t.Fatalf("clone calls = %d, want 1", spy.cloneCalls)
	}
	if spy.cloneStorageURL != testSborkaCommand("").StorageUrl {
		t.Fatalf("unexpected clone storage url: %q", spy.cloneStorageURL)
	}
	if lastRecord.BuiltAt == nil || *lastRecord.BuiltAt != buildStateTestBuiltAt {
		t.Fatalf("unexpected builtAt: %#v", lastRecord.BuiltAt)
	}
	if lastRecord.Commit == nil || *lastRecord.Commit != "abc123" {
		t.Fatalf("unexpected commit: %#v", lastRecord.Commit)
	}
	if lastRecord.Branch == nil || *lastRecord.Branch != "main" {
		t.Fatalf("unexpected branch: %#v", lastRecord.Branch)
	}

	wantPath := "/api/runner/runner-cmd/projects/project-1/build-state"
	if len(paths) < 2 || paths[0] != wantPath || paths[1] != wantPath {
		t.Fatalf("unexpected backend paths: %#v", paths)
	}

	if !containsCommand(spy.execCalls, "git fetch --prune origin +refs/heads/feature/x:refs/remotes/origin/feature/x") {
		t.Fatalf("unit branch fetch was not executed: %#v", spy.execCalls)
	}

	// Токен из StorageUrl не должен попадать в шаги активности.
	for _, step := range result.Steps {
		if strings.Contains(step.Command, "token") || strings.Contains(step.Response, "token") {
			t.Fatalf("token leaked into step: %#v", step)
		}
	}
}

func TestNachatSborkuUnitaSkipsCloneWhenAlreadyBuilt(t *testing.T) {
	backend := &fakeBuildStateBackend{
		secret: buildStateTestSecret,
		built:  true,
		commit: "abc123",
		branch: "main",
	}
	server := httptest.NewServer(backend)
	defer server.Close()

	spy := &activityTestSpy{}
	deps := newSborkaTestDeps(t, server.URL, buildStateTestSecret, spy, nil)

	result, err := nachatSborkuUnitaActivity(context.Background(), testSborkaCommand("runner-cmd"), deps)
	if err != nil {
		t.Fatalf("activity error: %v", err)
	}
	if result.Success != 1 {
		t.Fatalf("expected success, steps: %#v", result.Steps)
	}

	_, puts, authFailures, _, _ := backend.snapshot()
	if authFailures != 0 {
		t.Fatalf("signature failures: %d", authFailures)
	}
	if spy.cloneCalls != 0 {
		t.Fatalf("clone must be skipped, got %d calls", spy.cloneCalls)
	}
	if puts != 0 {
		t.Fatalf("PUT must be skipped, got %d calls", puts)
	}
	if !containsCommand(spy.execCalls, "git fetch --prune origin +refs/heads/feature/x:refs/remotes/origin/feature/x") {
		t.Fatalf("unit branch fetch was not executed: %#v", spy.execCalls)
	}
}

func TestNachatSborkuUnitaResolvesRunnerIDFromConfig(t *testing.T) {
	backend := &fakeBuildStateBackend{secret: buildStateTestSecret, built: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	spy := &activityTestSpy{}
	deps := newSborkaTestDeps(t, server.URL, buildStateTestSecret, spy, nil)

	if _, err := nachatSborkuUnitaActivity(context.Background(), testSborkaCommand(""), deps); err != nil {
		t.Fatalf("activity error: %v", err)
	}

	_, _, _, _, paths := backend.snapshot()
	wantPath := "/api/runner/runner-config/projects/project-1/build-state"
	if len(paths) == 0 || paths[0] != wantPath {
		t.Fatalf("expected runnerId from config, paths: %#v", paths)
	}
}

func TestNachatSborkuUnitaBuildStateErrors(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		code         string
		wantContains string
		check        func(*ProjectBuildStateAPIError) bool
	}{
		{
			name:         "invalid project id",
			status:       http.StatusBadRequest,
			code:         ProjectBuildStateInvalidProjectIDCode,
			wantContains: ProjectBuildStateInvalidProjectIDCode,
			check: func(e *ProjectBuildStateAPIError) bool {
				return e.Code == ProjectBuildStateInvalidProjectIDCode
			},
		},
		{
			name:         "invalid payload",
			status:       http.StatusBadRequest,
			code:         ProjectBuildStateInvalidPayloadCode,
			wantContains: ProjectBuildStateInvalidPayloadCode,
			check: func(e *ProjectBuildStateAPIError) bool {
				return e.Code == ProjectBuildStateInvalidPayloadCode
			},
		},
		{
			name:         "invalid signature",
			status:       http.StatusUnauthorized,
			code:         RunnerInvalidSignatureCode,
			wantContains: RunnerInvalidSignatureCode,
			check:        func(e *ProjectBuildStateAPIError) bool { return e.IsInvalidSignature() },
		},
		{
			name:         "runner not found",
			status:       http.StatusNotFound,
			code:         RunnerNotFoundCode,
			wantContains: RunnerNotFoundCode,
			check:        func(e *ProjectBuildStateAPIError) bool { return e.IsRunnerNotFound() },
		},
		{
			name:         "runner inactive",
			status:       http.StatusConflict,
			code:         RunnerInactiveCode,
			wantContains: RunnerInactiveCode,
			check:        func(e *ProjectBuildStateAPIError) bool { return e.IsRunnerInactive() },
		},
		{
			name:         "auth disabled",
			status:       http.StatusServiceUnavailable,
			code:         RunnerAuthDisabledCode,
			wantContains: RunnerAuthDisabledCode,
			check:        func(e *ProjectBuildStateAPIError) bool { return e.IsAuthDisabled() },
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			backend := &fakeBuildStateBackend{
				secret:     buildStateTestSecret,
				failStatus: testCase.status,
				failCode:   testCase.code,
			}
			server := httptest.NewServer(backend)
			defer server.Close()

			spy := &activityTestSpy{commit: "abc123", branch: "main"}
			deps := newSborkaTestDeps(t, server.URL, buildStateTestSecret, spy, nil)

			result, err := nachatSborkuUnitaActivity(context.Background(), testSborkaCommand("runner-cmd"), deps)
			if err == nil {
				t.Fatal("expected activity error")
			}
			if result.Success != 0 {
				t.Fatalf("expected failure result, steps: %#v", result.Steps)
			}
			if !strings.Contains(err.Error(), testCase.wantContains) {
				t.Fatalf("error %q does not contain %q", err.Error(), testCase.wantContains)
			}

			apiErr, ok := AsProjectBuildStateAPIError(err)
			if !ok || !testCase.check(apiErr) {
				t.Fatalf("expected typed error for %s, got %v", testCase.code, err)
			}

			// Ошибка build-state не должна приводить к clone или PUT.
			_, puts, _, _, _ := backend.snapshot()
			if spy.cloneCalls != 0 || puts != 0 {
				t.Fatalf("no clone/PUT expected on backend error: clone=%d puts=%d", spy.cloneCalls, puts)
			}
		})
	}
}

func TestNachatSborkuUnitaRetryIsIdempotent(t *testing.T) {
	backend := &fakeBuildStateBackend{secret: buildStateTestSecret}
	server := httptest.NewServer(backend)
	defer server.Close()

	spy := &activityTestSpy{commit: "abc123", branch: "main"}
	deps := newSborkaTestDeps(t, server.URL, buildStateTestSecret, spy, nil)

	first, err := nachatSborkuUnitaActivity(context.Background(), testSborkaCommand("runner-cmd"), deps)
	if err != nil || first.Success != 1 {
		t.Fatalf("first run failed: %v, steps: %#v", err, first)
	}

	firstGets, firstPuts, _, _, _ := backend.snapshot()
	if spy.cloneCalls != 1 || firstPuts != 1 {
		t.Fatalf("first run must clone and PUT: clone=%d puts=%d", spy.cloneCalls, firstPuts)
	}

	second, err := nachatSborkuUnitaActivity(context.Background(), testSborkaCommand("runner-cmd"), deps)
	if err != nil || second.Success != 1 {
		t.Fatalf("second run failed: %v, steps: %#v", err, second)
	}

	secondGets, secondPuts, _, _, _ := backend.snapshot()
	if spy.cloneCalls != 1 {
		t.Fatalf("retry must not clone again: %d calls", spy.cloneCalls)
	}
	if secondPuts != 1 {
		t.Fatalf("retry must not PUT again: %d PUTs", secondPuts)
	}
	if secondGets != firstGets+1 {
		t.Fatalf("retry must re-check build-state: GETs %d -> %d", firstGets, secondGets)
	}
}

func TestNachatSborkuUnitaBuildStateTimeout(t *testing.T) {
	backend := &fakeBuildStateBackend{
		secret: buildStateTestSecret,
		delay:  100 * time.Millisecond,
	}
	server := httptest.NewServer(backend)
	defer server.Close()

	spy := &activityTestSpy{commit: "abc123", branch: "main"}
	deps := newSborkaTestDeps(t, server.URL, buildStateTestSecret, spy, &http.Client{Timeout: 10 * time.Millisecond})

	result, err := nachatSborkuUnitaActivity(context.Background(), testSborkaCommand("runner-cmd"), deps)
	if err == nil {
		t.Fatal("expected activity error on timeout")
	}
	if result.Success != 0 {
		t.Fatalf("expected failure result, steps: %#v", result.Steps)
	}
	if spy.cloneCalls != 0 {
		t.Fatalf("timeout must not trigger clone: %d", spy.cloneCalls)
	}
}

func containsCommand(commands []string, want string) bool {
	for _, command := range commands {
		if command == want {
			return true
		}
	}

	return false
}
