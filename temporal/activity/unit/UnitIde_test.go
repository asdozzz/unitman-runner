package unit

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIdeHealthCheckCommandUsesNamespaceLocalhost(t *testing.T) {
	command := IdeHealthCheckCommand()

	if !strings.Contains(command, "http://127.0.0.1:47000/healthz") {
		t.Fatalf("команда проверки IDE должна обращаться к локальному health-эндпоинту: %q", command)
	}
	if IdeHealthURL() != "http://127.0.0.1:47000/healthz" {
		t.Fatalf("неожиданный health URL: %q", IdeHealthURL())
	}
}

func TestWaitIdeReadyReturnsNilWhenProbeSucceeds(t *testing.T) {
	calls := 0
	exec := func(unitPath, command string) (string, error) {
		calls++
		if unitPath != "/unit" {
			t.Fatalf("неожиданный каталог юнита: %q", unitPath)
		}
		if command != IdeHealthCheckCommand() {
			t.Fatalf("неожиданная команда проверки: %q", command)
		}
		if calls < 3 {
			return "", errors.New("connection refused")
		}
		return "", nil
	}

	if err := waitIdeReady("/unit", time.Second, time.Millisecond, exec); err != nil {
		t.Fatalf("ожидался успех готовности IDE, получено: %v", err)
	}
	if calls != 3 {
		t.Fatalf("проверка IDE выполнена %d раз, ожидалось 3", calls)
	}
}

func TestWaitIdeReadyFailsOnTimeout(t *testing.T) {
	calls := 0
	exec := func(string, string) (string, error) {
		calls++
		return "", errors.New("connection refused")
	}

	err := waitIdeReady("/unit", 20*time.Millisecond, 5*time.Millisecond, exec)
	if err == nil {
		t.Fatal("ожидалась ошибка по таймауту готовности IDE")
	}
	if !strings.Contains(err.Error(), "IDE не стала готова") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("ошибка должна содержать последнюю причину: %v", err)
	}
	if calls < 2 {
		t.Fatalf("ожидалось несколько попыток проверки, выполнено %d", calls)
	}
}

func TestWaitIdeReadyRejectsEmptyUnitPath(t *testing.T) {
	exec := func(string, string) (string, error) {
		t.Fatal("исполнитель команд не должен вызываться")
		return "", nil
	}

	if err := waitIdeReady("   ", time.Second, time.Millisecond, exec); err == nil {
		t.Fatal("ожидалась ошибка пустого каталога юнита")
	}
}

func TestWaitIdeReadyRejectsNonPositiveTimeout(t *testing.T) {
	exec := func(string, string) (string, error) {
		t.Fatal("исполнитель команд не должен вызываться")
		return "", nil
	}

	if err := waitIdeReady("/unit", 0, time.Millisecond, exec); err == nil {
		t.Fatal("ожидалась ошибка неположительного таймаута")
	}
}

func TestWaitIdeReadyRejectsNilExecutor(t *testing.T) {
	if err := waitIdeReady("/unit", time.Second, time.Millisecond, nil); err == nil {
		t.Fatal("ожидалась ошибка отсутствующего исполнителя команд")
	}
}

func TestIdeGitIndexCommandCopiesIndexInsideIde(t *testing.T) {
	want := []string{
		"docker", "compose", "exec", "-T", "ide",
		"sh", "-c", "cp /home/coder/project/.git/index /tmp/git-index",
	}

	if got := IdeGitIndexCommand(); !reflect.DeepEqual(got, want) {
		t.Fatalf("неожиданный argv обновления git-индекса: %#v", got)
	}
}

func TestRefreshIdeGitIndexCopiesIndex(t *testing.T) {
	var (
		gotDir  string
		gotArgs []string
	)

	exec := func(dir string, args []string) (string, error) {
		gotDir = dir
		gotArgs = args
		return "", nil
	}

	if err := refreshIdeGitIndex("/unit", exec); err != nil {
		t.Fatalf("ожидался успех обновления git-индекса, получено: %v", err)
	}
	if gotDir != "/unit" {
		t.Fatalf("неожиданный каталог юнита: %q", gotDir)
	}
	if want := IdeGitIndexCommand(); !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("неожиданный argv: %#v", gotArgs)
	}
}

func TestRefreshIdeGitIndexFailsWhenIndexMissing(t *testing.T) {
	exec := func(string, []string) (string, error) {
		return "", errors.New("cp: cannot stat '/home/coder/project/.git/index': No such file or directory")
	}

	err := refreshIdeGitIndex("/unit", exec)
	if err == nil {
		t.Fatal("ожидалась ошибка отсутствующего .git/index")
	}
	if !strings.Contains(err.Error(), "git-индекс IDE") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !strings.Contains(err.Error(), "No such file or directory") {
		t.Fatalf("ошибка должна содержать причину от cp: %v", err)
	}
}

func TestRefreshIdeGitIndexRejectsEmptyUnitPath(t *testing.T) {
	exec := func(string, []string) (string, error) {
		t.Fatal("исполнитель команд не должен вызываться")
		return "", nil
	}

	if err := refreshIdeGitIndex("   ", exec); err == nil {
		t.Fatal("ожидалась ошибка пустого каталога юнита")
	}
}

func TestRefreshIdeGitIndexRejectsNilExecutor(t *testing.T) {
	if err := refreshIdeGitIndex("/unit", nil); err == nil {
		t.Fatal("ожидалась ошибка отсутствующего исполнителя команд")
	}
}
