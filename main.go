package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"runner/config"
	"runner/temporal"
	"runner/temporal/activity/unit"
)

const (
	logPath      = "runner.log"
	pidPath      = "runner.pid"
	logTailLines = 200
	stopTimeout  = 15 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "init":
		if err := runInit(os.Stdin, os.Stdout, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "init:", err)
			os.Exit(1)
		}
	case "start":
		foreground, err := parseStartArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "start:", err)
			os.Exit(2)
		}
		if foreground {
			if err := runWorker(); err != nil {
				fmt.Fprintln(os.Stderr, "start:", err)
				os.Exit(1)
			}
		} else if err := runStart(); err != nil {
			fmt.Fprintln(os.Stderr, "start:", err)
			os.Exit(1)
		}
	case "stop":
		if err := runStop(); err != nil {
			fmt.Fprintln(os.Stderr, "stop:", err)
			os.Exit(1)
		}
	case "logs":
		if err := runLogs(); err != nil {
			fmt.Fprintln(os.Stderr, "logs:", err)
			os.Exit(1)
		}
	case "exec":
		if err := runExec(os.Stdout, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "exec:", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runInit(in io.Reader, out io.Writer, args []string) error {
	host := ""
	if len(args) > 0 {
		host = strings.TrimSpace(args[0])
	}

	if host == "" {
		fmt.Fprint(out, "Host [localhost]: ")
		reader := bufio.NewReader(in)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		host = strings.TrimSpace(line)
	}

	if host == "" {
		host = "localhost"
	}

	if err := config.CreateConfig(config.ConfigTemplatePath, config.ConfigPath, host); err != nil {
		return err
	}

	fmt.Fprintf(out, "Создан %s для host %s\n", config.ConfigPath, host)
	return nil
}

func parseStartArgs(args []string) (foreground bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--foreground", "-f":
			foreground = true
		default:
			return false, fmt.Errorf("неизвестный флаг %q", arg)
		}
	}
	return foreground, nil
}

// parseExecArgs разбирает аргументы команды exec:
// <ProjectId> <UnitId> <команда...>. Команда может содержать пробелы и
// передаётся в контейнер юнита целиком.
func parseExecArgs(args []string) (projectID, unitID, command string, err error) {
	if len(args) < 3 {
		return "", "", "", fmt.Errorf("нужно указать ProjectId, UnitId и команду")
	}

	projectID = strings.TrimSpace(args[0])
	unitID = strings.TrimSpace(args[1])
	command = strings.TrimSpace(strings.Join(args[2:], " "))

	if projectID == "" || unitID == "" || command == "" {
		return "", "", "", fmt.Errorf("ProjectId, UnitId и команда не должны быть пустыми")
	}

	return projectID, unitID, command, nil
}

// runExec выполняет команду внутри контейнера юнита и печатает её вывод.
func runExec(out io.Writer, args []string) error {
	projectID, unitID, command, err := parseExecArgs(args)
	if err != nil {
		return err
	}

	result, err := unit.RunUnitCommand(projectID, unitID, command)
	if err != nil {
		return err
	}

	fmt.Fprint(out, result)
	if result != "" && !strings.HasSuffix(result, "\n") {
		fmt.Fprintln(out)
	}

	return nil
}

// runWorker запускает обработчик Temporal в текущем процессе.
func runWorker() error {
	configuration, err := config.ParseConfig()
	if err != nil {
		return err
	}

	fmt.Printf("Runner запущен (pid %d)\n", os.Getpid())
	temporal.Init(configuration)
	return nil
}

// runStart проверяет конфиг и запускает обработчик в отдельном фоне, чтобы
// команда сразу вернула управление.
func runStart() error {
	if _, err := config.ParseConfig(); err != nil {
		return err
	}

	if pid, err := readPID(pidPath); err == nil {
		if processAlive(pid) {
			return fmt.Errorf("runner уже запущен (pid %d)", pid)
		}
		_ = os.Remove(pidPath)
	} else if !os.IsNotExist(err) {
		_ = os.Remove(pidPath)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("определить исполняемый файл: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("открыть лог %s: %w", logPath, err)
	}
	defer logFile.Close()

	cmd := exec.Command(executable, "start", "--foreground")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запустить runner: %w", err)
	}

	pid := cmd.Process.Pid
	if err := writePID(pidPath, pid); err != nil {
		_ = cmd.Process.Kill()
		return err
	}

	// Даём процессу шанс сразу упасть (например, из-за конфига), чтобы
	// вернуть ошибку вместо сообщения об успешном запуске.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		_ = os.Remove(pidPath)
		if err != nil {
			return fmt.Errorf("runner завершился сразу после запуска: %w (смотрите %s)", err, logPath)
		}
		return fmt.Errorf("runner завершился сразу после запуска (смотрите %s)", logPath)
	case <-time.After(250 * time.Millisecond):
	}

	fmt.Printf("Runner запущен в фоне (pid %d), лог: %s\n", pid, logPath)
	return nil
}

// runStop останавливает обработчик Temporal, дожидаясь штатного завершения.
func runStop() error {
	pid, err := readPID(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Runner не запущен.")
			return nil
		}
		return err
	}

	if !processAlive(pid) {
		_ = os.Remove(pidPath)
		fmt.Println("Runner не запущен (удалён устаревший pid-файл).")
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			_ = os.Remove(pidPath)
			fmt.Println("Runner не запущен.")
			return nil
		}
		return fmt.Errorf("отправить SIGTERM процессу %d: %w", pid, err)
	}

	if waitForExit(pid, stopTimeout) {
		_ = os.Remove(pidPath)
		fmt.Printf("Runner остановлен (pid %d).\n", pid)
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("принудительно остановить процесс %d: %w", pid, err)
	}
	waitForExit(pid, 2*time.Second)
	_ = os.Remove(pidPath)

	fmt.Printf("Runner остановлен принудительно (pid %d).\n", pid)
	return nil
}

// runLogs печатает последние строки лога.
func runLogs() error {
	lines, err := tailFile(logPath, logTailLines)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("лог %s не найден (runner ещё не запускался?)", logPath)
		}
		return err
	}

	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("некорректный pid в %s", path)
	}
	return pid, nil
}

func writePID(path string, pid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0644); err != nil {
		return fmt.Errorf("записать pid-файл %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("записать pid-файл %s: %w", path, err)
	}
	return nil
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !processAlive(pid)
}

// tailFile читает файл с конца и возвращает не больше count последних строк.
func tailFile(path string, count int) ([]string, error) {
	if count <= 0 {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	const chunkSize = 32 * 1024
	offset := info.Size()
	var data []byte
	newlines := 0

	for offset > 0 && newlines <= count {
		size := int64(chunkSize)
		if offset < size {
			size = offset
		}
		offset -= size

		chunk := make([]byte, size)
		read, err := file.ReadAt(chunk, offset)
		if err != nil && err != io.EOF {
			return nil, err
		}
		chunk = chunk[:read]
		data = append(chunk, data...)
		newlines += bytes.Count(chunk, []byte{'\n'})
	}

	// Если читать начали не с начала файла, первая строка может быть обрезана.
	if offset > 0 {
		var previous [1]byte
		if _, err := file.ReadAt(previous[:], offset-1); err != nil {
			return nil, err
		}
		if previous[0] != '\n' {
			if index := bytes.IndexByte(data, '\n'); index >= 0 {
				data = data[index+1:]
			} else {
				data = nil
			}
		}
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return lines, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `Unitman runner

Использование:
  unitman-runner init [host]   создать config.json из config.json.dist
  unitman-runner start         запустить обработчик Temporal в фоне
  unitman-runner stop          остановить обработчик Temporal
  unitman-runner logs          показать последние 200 строк лога
  unitman-runner exec <ProjectId> <UnitId> <команда>
                               выполнить команду внутри контейнера юнита

Если host не указан, init спросит его. Пустой ответ — localhost.
start --foreground запускает обработчик в текущем процессе.`)
}
