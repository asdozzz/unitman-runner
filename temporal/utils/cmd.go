package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// urlCredentialPattern находит userinfo в URL, чтобы токены не попадали в
// сообщения об ошибках и логи команд.
var urlCredentialPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]*@`)

// RedactURLCredentials заменяет логин/токен в URL на `redacted`.
func RedactURLCredentials(value string) string {
	return urlCredentialPattern.ReplaceAllString(value, "${1}redacted@")
}

// commandTimeout — максимальное время выполнения одной команды.
const commandTimeout = 30 * time.Minute

// ErrInteractiveInput возвращается, если команда запросила интерактивный ввод.
var ErrInteractiveInput = errors.New("команда ожидает интерактивного ввода")

// Приглашения, по которым можно распознать ожидание ввода.
// Все строки должны быть в нижнем регистре: вывод команды тоже приводится к нижнему регистру.
var interactivePrompts = []string{
	"password:",
	"password for",
	"passphrase:",
	"passphrase for",
	"username:",
	"username for",
	"login:",
	"enter password",
	"enter passphrase",
	"enter username",
	"are you sure",
	"do you want to continue",
	"[y/n]",
	"(y/n)",
	"[yes/no]",
	"(yes/no)",
	"continue?",
	"press any key",
	"press enter",
	"hit enter",
	"type 'yes'",
	"type yes",
	"overwrite?",
	"do you want",
	"proceed?",
	"введите пароль",
	"введите логин",
	"подтвердите",
	"требуется ввод",
}

type promptDetector struct {
	mu      sync.Mutex
	tail    []byte
	match   string
	onMatch func()
}

func (d *promptDetector) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.tail = append(d.tail, bytes.ToLower(p)...)
	if len(d.tail) > 8192 {
		d.tail = d.tail[len(d.tail)-8192:]
	}

	if d.match == "" {
		for _, prompt := range interactivePrompts {
			if bytes.Contains(d.tail, []byte(prompt)) {
				d.match = prompt
				if d.onMatch != nil {
					d.onMatch()
				}
				break
			}
		}
	}

	return len(p), nil
}

func (d *promptDetector) Match() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.match
}

func ExecCommandOld(workingDirectory string, app []string) (string, error) {
	x := app[0]
	_, app = app[0], app[1:]
	cmd := exec.Command(x, app...)
	cmd.Dir = workingDirectory
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("cmd.Run() failed with %s", err)
		return "", errors.New(fmt.Sprintf("%s", err))
	}

	return string(out), nil
}

func ExecCommand(workingDirectory string, app []string) (string, error) {
	return ExecCommandWithTimeout(workingDirectory, app, commandTimeout)
}

func ExecCommandWithTimeout(workingDirectory string, app []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return execCommand(ctx, workingDirectory, app, timeout, cancel)
}

// ExecCommandContext выполняет команду с привязкой к родительскому контексту.
// Отмена контекста завершает всю группу процессов команды.
func ExecCommandContext(ctx context.Context, workingDirectory string, app []string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return execCommand(ctx, workingDirectory, app, 0, cancel)
}

func execCommand(ctx context.Context, workingDirectory string, app []string, timeout time.Duration, cancel func()) (string, error) {
	if cancel == nil {
		cancel = func() {}
	}

	cmd := exec.Command(app[0], app[1:]...)
	cmd.Dir = workingDirectory
	// Ввод не поддерживаем: дочерний процесс читает из /dev/null и сразу получает EOF.
	cmd.Stdin = nil
	// Отдельная группа процессов, чтобы по таймауту завершить команду вместе с дочерними процессами.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	detector := &promptDetector{onMatch: cancel}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf, detector)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf, detector)

	if err := cmd.Start(); err != nil {
		return "", err
	}

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()

	var runErr error
	select {
	case runErr = <-waitErr:
	case <-ctx.Done():
		select {
		case runErr = <-waitErr:
			// Команда успела завершиться сама.
		default:
			// Завершаем всю группу процессов: одного docker-клиента недостаточно.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			runErr = <-waitErr
		}
	}

	outStr := stdoutBuf.String() + stderrBuf.String()
	command := RedactURLCredentials(strings.Join(app, " "))

	if prompt := detector.Match(); prompt != "" {
		return outStr, fmt.Errorf("%w: обнаружено приглашение %q, команда: %s", ErrInteractiveInput, prompt, command)
	}

	if ctx.Err() == context.DeadlineExceeded {
		if timeout > 0 {
			return outStr, fmt.Errorf("команда не завершилась за %s (возможно, ожидает ввода): %s", timeout, command)
		}
		return outStr, fmt.Errorf("команда не завершилась за отведённое время (возможно, ожидает ввода): %s", command)
	}

	if ctx.Err() == context.Canceled {
		return outStr, fmt.Errorf("выполнение команды прервано (возможно, ожидает ввода): %s", command)
	}

	if runErr != nil {
		safeOut := RedactURLCredentials(outStr)
		log.Printf("cmd.Run() failed with %s\n - out: %s", runErr, safeOut)
		if strings.TrimSpace(outStr) == "" {
			return "", runErr
		}
		return "", errors.New(safeOut)
	}

	return outStr, nil
}

func copyAndCapture(w io.Writer, r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 2048, 2048)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			d := buf[:n]
			out = append(out, d...)
			_, err := w.Write(d)
			if err != nil {
				return out, err
			}
		}
		if err != nil {
			// Read returns io.EOF at the end of file, which is not an error for us
			if err == io.EOF {
				err = nil
			}
			return out, err
		}
	}
}
