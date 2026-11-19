package monitor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"runner/state"
)

// fakeDockerScript эмулирует docker для reconciler/bootstrap.
// Поведение управляется переменными окружения:
//   - FAKE_DOCKER_PS_FAIL != "" — docker ps завершается с кодом 1;
//   - FAKE_DOCKER_PS_FILE — файл, содержимое которого выводится для docker ps.
//
// Остальные подкоманды (например, docker stats) завершаются успешно без вывода.
const fakeDockerScript = `#!/bin/sh
if [ "$1" = "ps" ]; then
	if [ -n "$FAKE_DOCKER_PS_FAIL" ]; then
		echo "fake docker ps error" >&2
		exit 1
	fi
	if [ -n "$FAKE_DOCKER_PS_FILE" ] && [ -f "$FAKE_DOCKER_PS_FILE" ]; then
		cat "$FAKE_DOCKER_PS_FILE"
	fi
	exit 0
fi
exit 0
`

// installFakeDocker кладёт исполняемый скрипт docker во временный каталог и
// ставит этот каталог первым в PATH.
func installFakeDocker(t *testing.T, script string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// notificationSink — httptest-приёмник уведомлений reconciler.
type notificationSink struct {
	mu       sync.Mutex
	payloads []NotificationsPayload
}

func (s *notificationSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != notificationsPath {
		http.Error(w, "unexpected path", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var payload NotificationsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "decode body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.payloads = append(s.payloads, payload)
	s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (s *notificationSink) notifications() []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Notification, 0)
	for _, payload := range s.payloads {
		result = append(result, payload.Notifications...)
	}

	return result
}

// writePSFile перезаписывает вывод fake docker ps.
func writePSFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write docker ps output: %v", err)
	}
}

func TestReconcileOnceReportsMismatchAndResolutionWithoutSpam(t *testing.T) {
	sink := &notificationSink{}
	server := httptest.NewServer(sink)
	defer server.Close()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()

	workdir := t.TempDir()
	psFile := filepath.Join(workdir, "ps.json")
	writePSFile(t, psFile, "")
	t.Setenv("FAKE_DOCKER_PS_FILE", psFile)
	installFakeDocker(t, fakeDockerScript)

	client := &Client{
		BaseURL:    server.URL,
		RunnerID:   "runner-1",
		HTTPClient: server.Client(),
	}

	unit := state.UnitState{
		UnitID:        "unit-1",
		ProjectID:     "project-1",
		UnitName:      "unit-one",
		ProjectName:   "project-one",
		ContainerName: "unit-one.project-one",
		Expected:      state.ExpectedRunning,
	}
	if err := store.Upsert(unit); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Итерация 1: ожидается running, контейнера нет — mismatch.
	reconcileOnce(context.Background(), store, client, workdir)

	notes := sink.notifications()
	if len(notes) != 1 {
		t.Fatalf("first reconcile: expected 1 notification, got %d: %+v", len(notes), notes)
	}
	if notes[0].EventType != "mismatch" {
		t.Errorf("first reconcile: eventType = %q, want mismatch", notes[0].EventType)
	}
	if notes[0].ActualState != string(state.ActualAbsent) {
		t.Errorf("first reconcile: actualState = %q, want absent", notes[0].ActualState)
	}
	if notes[0].ExpectedState != string(state.ExpectedRunning) {
		t.Errorf("first reconcile: expectedState = %q, want running", notes[0].ExpectedState)
	}

	stored, found, err := store.Get("unit-1")
	if err != nil || !found {
		t.Fatalf("Get after first reconcile: found=%v err=%v", found, err)
	}
	if stored.Actual != state.ActualAbsent {
		t.Errorf("persisted actual = %q, want absent", stored.Actual)
	}
	if stored.MismatchSince == 0 {
		t.Errorf("MismatchSince must be set on mismatch")
	}

	// Итерация 2: состояние не изменилось — новых уведомлений быть не должно.
	reconcileOnce(context.Background(), store, client, workdir)
	if got := len(sink.notifications()); got != 1 {
		t.Fatalf("second reconcile must not send notifications, got %d total", got)
	}

	// Итерация 3: контейнер снова running — resolved.
	writePSFile(t, psFile, `{"Names":"unit-one.project-one","State":"running"}`+"\n")
	reconcileOnce(context.Background(), store, client, workdir)

	notes = sink.notifications()
	if len(notes) != 2 {
		t.Fatalf("third reconcile: expected 2 notifications total, got %d: %+v", len(notes), notes)
	}
	if notes[1].EventType != "resolved" {
		t.Errorf("third reconcile: eventType = %q, want resolved", notes[1].EventType)
	}
	if notes[1].ActualState != string(state.ActualRunning) {
		t.Errorf("third reconcile: actualState = %q, want running", notes[1].ActualState)
	}

	stored, found, err = store.Get("unit-1")
	if err != nil || !found {
		t.Fatalf("Get after third reconcile: found=%v err=%v", found, err)
	}
	if stored.Actual != state.ActualRunning {
		t.Errorf("persisted actual after recovery = %q, want running", stored.Actual)
	}
	if stored.MismatchSince != 0 {
		t.Errorf("MismatchSince must be reset on recovery, got %d", stored.MismatchSince)
	}
}

func TestReconcileOnceDockerErrorMarksUnknownAndDoesNotNotify(t *testing.T) {
	sink := &notificationSink{}
	server := httptest.NewServer(sink)
	defer server.Close()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()

	workdir := t.TempDir()
	t.Setenv("FAKE_DOCKER_PS_FAIL", "1")
	installFakeDocker(t, fakeDockerScript)

	client := &Client{
		BaseURL:    server.URL,
		RunnerID:   "runner-1",
		HTTPClient: server.Client(),
	}

	if err := store.Upsert(state.UnitState{
		UnitID:        "unit-1",
		ContainerName: "unit-one.project-one",
		Expected:      state.ExpectedRunning,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reconcileOnce(context.Background(), store, client, workdir)

	if notes := sink.notifications(); len(notes) != 0 {
		t.Fatalf("docker ps error must not produce notifications, got %+v", notes)
	}

	stored, found, err := store.Get("unit-1")
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if stored.Actual != state.ActualUnknown {
		t.Errorf("actual = %q, want unknown", stored.Actual)
	}
	if stored.MismatchSince != 0 {
		t.Errorf("MismatchSince must stay 0 on unknown, got %d", stored.MismatchSince)
	}
}
