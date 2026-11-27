package opencode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"runner/config"
)

func TestValidArtifactName(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"../x.md", false},
		{"a/b.md", false},
		{".hidden.md", false},
		{"..", false},
		{"x", false},
		{"x.html", false},
		{"x.svg", false},
		{"x.sql", false},
		{"x.md", true},
		{"X.MD", true},
		{strings.Repeat("a", 124) + ".md", true},  // 127 символов
		{strings.Repeat("a", 126) + ".md", false}, // 129 символов
	}

	for _, tc := range cases {
		if got := validArtifactName(tc.name); got != tc.valid {
			t.Fatalf("validArtifactName(%q) = %v, want %v", tc.name, got, tc.valid)
		}
	}
}

// fakeArtifactExec собирает find-вывод из карты имя->размер.
func fakeArtifactExec(t *testing.T, entries []artifactFile, execErr error) (func(context.Context, string, string, string, []string) (string, error), *[]string) {
	t.Helper()

	var (
		mu       sync.Mutex
		commands []string
	)

	exec := func(_ context.Context, _, command, _ string, _ []string) (string, error) {
		mu.Lock()
		commands = append(commands, command)
		mu.Unlock()

		if execErr != nil {
			return "", execErr
		}

		var builder strings.Builder
		for _, entry := range entries {
			builder.WriteString(entry.Name)
			builder.WriteString("\t")
			builder.WriteString(itoa(entry.Size))
			builder.WriteString("\n")
		}

		return builder.String(), nil
	}

	return exec, &commands
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func TestCollectWorkspaceArtifactsFiltersAndSorts(t *testing.T) {
	entries := []artifactFile{
		{Name: "b.md", Size: 10},
		{Name: "a.txt", Size: 20},
		{Name: "bad.html", Size: 5},
		{Name: "empty.log", Size: 0},
		{Name: "toolarge.json", Size: maxArtifactBytes + 1},
	}

	exec, _ := fakeArtifactExec(t, entries, nil)
	files, err := collectWorkspaceArtifacts(context.Background(), "/host", "/workspace/out", exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("unexpected files: %+v", files)
	}
	if files[0].Name != "a.txt" || files[1].Name != "b.md" {
		t.Fatalf("files must be sorted by name: %+v", files)
	}
}

func TestCollectWorkspaceArtifactsLimits(t *testing.T) {
	entries := []artifactFile{
		{Name: "a.md", Size: 1},
		{Name: "b.md", Size: 1},
		{Name: "c.md", Size: 1},
		{Name: "d.md", Size: 1},
		{Name: "e.md", Size: 1},
		{Name: "f.md", Size: 1},
	}
	exec, _ := fakeArtifactExec(t, entries, nil)
	files, err := collectWorkspaceArtifacts(context.Background(), "/host", "/workspace/out", exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != maxArtifacts {
		t.Fatalf("must stop at %d files, got %d", maxArtifacts, len(files))
	}
}

func TestCollectWorkspaceArtifactsTotalLimit(t *testing.T) {
	// Каждый файл по 2 МБ: пятый выходит за суммарный лимит 8 МБ.
	entries := make([]artifactFile, 0, 6)
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md", "e.md"} {
		entries = append(entries, artifactFile{Name: name, Size: maxArtifactBytes})
	}
	exec, _ := fakeArtifactExec(t, entries, nil)
	files, err := collectWorkspaceArtifacts(context.Background(), "/host", "/workspace/out", exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != maxTotalArtifactBytes/maxArtifactBytes {
		t.Fatalf("total limit must cap files, got %d", len(files))
	}
}

func TestCollectWorkspaceArtifactsMissingDirIsEmpty(t *testing.T) {
	exec, _ := fakeArtifactExec(t, nil, nil)
	files, err := collectWorkspaceArtifacts(context.Background(), "/host", "/workspace/out", exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("missing dir must produce empty slice: %+v", files)
	}
}

func TestCollectWorkspaceArtifactsExecErrorPropagated(t *testing.T) {
	exec, _ := fakeArtifactExec(t, nil, errors.New("docker unavailable"))
	if _, err := collectWorkspaceArtifacts(context.Background(), "/host", "/workspace/out", exec); err == nil {
		t.Fatal("exec error must be propagated")
	}
}

func TestCopyFileFromWorkspaceArgs(t *testing.T) {
	type captured struct {
		dir    string
		source string
		target string
	}
	var got captured

	previous := copyFileFromWorkspace
	copyFileFromWorkspace = func(_ context.Context, dir, source, target string) (string, error) {
		got = captured{dir: dir, source: source, target: target}
		return "", nil
	}
	defer func() { copyFileFromWorkspace = previous }()

	if _, err := copyFileFromWorkspace(context.Background(), "/host/ws", "/workspace/out/plan.md", "/tmp/x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.dir != "/host/ws" || got.source != "/workspace/out/plan.md" || got.target != "/tmp/x" {
		t.Fatalf("unexpected copy args: %+v", got)
	}
}

func TestArtifactURLVariants(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"https://example.test", "https://example.test/api/runner/opencode/artifact"},
		{"https://example.test/api", "https://example.test/api/runner/opencode/artifact"},
		{"https://example.test/api/runner", "https://example.test/api/runner/opencode/artifact"},
		{"https://example.test/api/runner/opencode/artifact", "https://example.test/api/runner/opencode/artifact"},
	}
	for _, tc := range cases {
		got := artifactURL(tc.base, "wf", "ws", "plan.md", "text/markdown")
		if !strings.HasPrefix(got, tc.want+"?") {
			t.Fatalf("artifactURL(%q) = %q, want prefix %q", tc.base, got, tc.want)
		}
	}

	encoded := artifactURL("https://example.test", "wf-1", "ws-1", "my plan.md", "text/markdown")
	if !strings.Contains(encoded, "name=my+plan.md") {
		t.Fatalf("name must be URL-encoded: %q", encoded)
	}
	if !strings.Contains(encoded, "contentType=text%2Fmarkdown") {
		t.Fatalf("contentType must be URL-encoded: %q", encoded)
	}
	if !strings.Contains(encoded, "workflowId=wf-1") || !strings.Contains(encoded, "workspaceId=ws-1") {
		t.Fatalf("query ids are missing: %q", encoded)
	}
}

func TestArtifactSenderSendSignatureAndParse(t *testing.T) {
	const (
		secret     = "secret"
		workflowID = "wf-1"
		name       = "my plan.md"
	)
	body := []byte("# План\n")

	var received struct {
		signature string
		runnerID  string
		body      []byte
		name      string
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.name = r.URL.Query().Get("name")
		received.runnerID = r.Header.Get(config.RunnerIDHeader)
		received.signature = r.Header.Get(config.RunnerSignatureHeader)
		received.body, _ = io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"fileId":"f-1","name":"my plan.md","size":7,"contentType":"text/markdown"}}`)
	}))
	defer server.Close()

	sender := newArtifactSender(server.Client(), server.URL, "runner-1", secret, workflowID)
	art, retry, err := sender.send(context.Background(), "ws-1", name, "text/markdown", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retry {
		t.Fatal("200 must not request retry")
	}
	if art.FileID != "f-1" || art.Name != "my plan.md" || art.Size != 7 || art.ContentType != "text/markdown" {
		t.Fatalf("unexpected artifact: %+v", art)
	}

	if string(received.body) != string(body) {
		t.Fatalf("body mismatch: %q", received.body)
	}
	if received.name != name {
		t.Fatalf("raw name not decoded by server: %q", received.name)
	}
	if received.runnerID != "runner-1" {
		t.Fatalf("unexpected runner id: %q", received.runnerID)
	}

	expected := config.Sign(secret, []byte(workflowID+"\n"+name+"\n"+string(body)))
	if received.signature != expected {
		t.Fatalf("signature = %q, want %q", received.signature, expected)
	}
}

func TestArtifactSenderSkipsEmptyWorkflow(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := newArtifactSender(server.Client(), server.URL, "runner-1", "secret", "")
	if _, retry, err := sender.send(context.Background(), "ws-1", "plan.md", "text/markdown", []byte("x")); err != nil || retry {
		t.Fatalf("empty workflow must be skipped without retry: retry=%v err=%v", retry, err)
	}
	if calls != 0 {
		t.Fatalf("empty workflow must not send requests, got %d", calls)
	}
}

func TestArtifactSenderRetryPolicy(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		sender := newArtifactSender(server.Client(), server.URL, "runner-1", "secret", "wf")
		_, retry, err := sender.send(context.Background(), "ws", "plan.md", "text/markdown", []byte("x"))
		server.Close()

		if err == nil {
			t.Fatalf("status %d must be an error", status)
		}
		if retry {
			t.Fatalf("status %d must not be retried", status)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	sender := newArtifactSender(server.Client(), server.URL, "runner-1", "secret", "wf")
	if _, retry, err := sender.send(context.Background(), "ws", "plan.md", "text/markdown", []byte("x")); err == nil || !retry {
		t.Fatalf("5xx must request retry: retry=%v err=%v", retry, err)
	}
}
