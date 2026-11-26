package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"runner/config"
)

// runUpdate реализует `unitman-runner update`: скачивает релизный образ
// <repo>:<version> из GHCR через docker, проверяет sha256 бинаря, атомарно
// подменяет текущий файл и перезапускает systemd-юнит. Регистрация в
// config.json и секреты не затрагиваются.
func runUpdate(out io.Writer, args []string) error {
	opts := updateOptions{
		repo: strings.TrimSpace(os.Getenv("UNITMAN_RELEASE_REPO")),
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version", "-V":
			i++
			if i >= len(args) {
				return fmt.Errorf("--version требует значение")
			}
			opts.version = strings.TrimSpace(args[i])
		case "--rollback":
			opts.rollback = true
		case "--check":
			opts.check = true
		case "--no-restart":
			opts.noRestart = true
		case "--home":
			i++
			if i >= len(args) {
				return fmt.Errorf("--home требует значение")
			}
			opts.home = strings.TrimSpace(args[i])
		case "--yes", "-y":
			opts.assumeYes = true
		case "-h", "--help":
			printUpdateUsage(out)
			return nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("неизвестный флаг %q", args[i])
			}
			opts.version = strings.TrimSpace(args[i])
		}
	}

	if opts.repo == "" {
		opts.repo = config.UpdateRepository
	}

	home := opts.home
	if home == "" {
		home = strings.TrimSpace(os.Getenv("UNITMAN_HOME"))
	}
	if home == "" {
		// Каталог установки — каталог самого бинаря; cwd может быть любым.
		if exe, exeErr := os.Executable(); exeErr == nil && filepath.Base(exe) == "unitman-runner" {
			home = filepath.Dir(exe)
		}
	}
	if home == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("определить рабочий каталог: %w", err)
		}
		home = cwd
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return fmt.Errorf("разрешить путь %s: %w", home, err)
	}

	binPath := filepath.Join(home, "unitman-runner")
	if exe, exeErr := os.Executable(); exeErr == nil && filepath.Base(exe) == "unitman-runner" {
		binPath = exe
	}

	statePath := filepath.Join(home, ".releases", "runner-state.env")
	backupBin := filepath.Join(home, "backups", "unitman-runner.prev")

	if opts.check {
		fmt.Fprintf(out, "Текущая версия: %s\n", binaryVersion(binPath))
		fmt.Fprintf(out, "Репозиторий релиза: %s\n", opts.repo)
		if opts.version != "" {
			fmt.Fprintf(out, "Запрошенная версия: %s\n", opts.version)
		}
		return nil
	}

	if opts.rollback {
		return rollbackUpdate(out, opts, binPath, statePath, backupBin)
	}

	if opts.version == "" {
		return fmt.Errorf("укажите --version vX.Y.Z (справка: update --help)")
	}

	oldVersion := binaryVersion(binPath)

	tmpRoot := filepath.Join(home, ".releases")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return fmt.Errorf("создать %s: %w", tmpRoot, err)
	}
	tmp, err := os.MkdirTemp(tmpRoot, ".update-*")
	if err != nil {
		return fmt.Errorf("создать временный каталог: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := pullReleaseImage(opts.repo, opts.version, tmp); err != nil {
		return err
	}
	if err := verifyReleaseChecksums(tmp, "unitman-runner"); err != nil {
		return err
	}

	newBin := filepath.Join(tmp, "unitman-runner")
	newVersion := binaryVersion(newBin)
	if newVersion == "" || newVersion == "unknown" {
		newVersion = opts.version
	}

	if newVersion == oldVersion {
		fmt.Fprintf(out, "Раннер уже на версии %s\n", oldVersion)
		return nil
	}

	if err := confirmUpdate(fmt.Sprintf("Обновить раннер с %s до %s?", oldVersion, newVersion), opts.assumeYes); err != nil {
		return err
	}

	dest := filepath.Join(tmpRoot, newVersion)
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("очистить %s: %w", dest, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("сохранить релиз в %s: %w", dest, err)
	}
	newBin = filepath.Join(dest, "unitman-runner")

	if err := os.MkdirAll(filepath.Dir(backupBin), 0o755); err != nil {
		return fmt.Errorf("создать каталог бэкапов: %w", err)
	}
	if _, err := os.Stat(binPath); err == nil {
		if err := copyFile(binPath, backupBin, 0o755); err != nil {
			return fmt.Errorf("сохранить предыдущий бинарь: %w", err)
		}
	}

	if err := replaceBinary(newBin, binPath); err != nil {
		return err
	}

	writeRunnerState(statePath, oldVersion, backupBin, newVersion)
	fmt.Fprintf(out, "Установлен раннер %s (было %s)\n", newVersion, oldVersion)
	fmt.Fprintf(out, "Откат: %s update --rollback\n", filepath.Base(binPath))

	return restartRunner(out, opts.noRestart)
}

func rollbackUpdate(out io.Writer, opts updateOptions, binPath, statePath, defaultPrev string) error {
	prevVersion, prevBin := readRunnerState(statePath)
	if prevBin == "" {
		prevBin = defaultPrev
	}
	if _, err := os.Stat(prevBin); err != nil {
		return fmt.Errorf("нет предыдущего бинаря %s: %w", prevBin, err)
	}

	label := prevVersion
	if label == "" {
		label = "предыдущую версию"
	}
	if err := confirmUpdate(fmt.Sprintf("Вернуть раннер на %s?", label), opts.assumeYes); err != nil {
		return err
	}

	if err := replaceBinary(prevBin, binPath); err != nil {
		return err
	}
	_ = os.Remove(statePath)
	fmt.Fprintf(out, "Возвращён бинарь %s\n", prevVersion)

	return restartRunner(out, opts.noRestart)
}

type updateOptions struct {
	repo      string
	version   string
	home      string
	rollback  bool
	check     bool
	noRestart bool
	assumeYes bool
}

func printUpdateUsage(out io.Writer) {
	fmt.Fprintln(out, `Использование: unitman-runner update [ОПЦИИ]

  --version vX.Y.Z   версия релиза (обязательна, если не --rollback/--check)
  --rollback         вернуть бинарь из последнего обновления
  --check            показать текущую версию и репозиторий релиза
  --home DIR         каталог установки (по умолчанию UNITMAN_HOME или cwd)
  --no-restart       не перезапускать systemd-юнит
  --yes, -y          без подтверждения
  -h, --help         эта справка

Релиз берётся из OCI-образа <repo>:<version> в GHCR. Нужен docker и
выполненный docker login ghcr.io. Репозиторий можно переопределить переменной
UNITMAN_RELEASE_REPO. config.json и регистрация не меняются.`)
}

func pullReleaseImage(repo, tag, dir string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker не найден в PATH: %w", err)
	}

	if err := runCommand("docker", "pull", repo+":"+tag); err != nil {
		return fmt.Errorf("docker pull %s:%s: %w (проверьте docker login ghcr.io)", repo, tag, err)
	}

	created, err := exec.Command("docker", "create", repo+":"+tag).Output()
	if err != nil {
		return fmt.Errorf("docker create %s:%s: %w", repo, tag, err)
	}
	container := strings.TrimSpace(string(created))
	defer func() { _ = runCommand("docker", "rm", "-f", container) }()

	if err := runCommand("docker", "cp", container+":/release/.", dir); err != nil {
		return fmt.Errorf("docker cp %s:/release: %w", container, err)
	}

	return nil
}

func verifyReleaseChecksums(dir, fileName string) error {
	checksums, err := os.Open(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("в релизе нет checksums.txt: %w", err)
	}
	defer checksums.Close()

	expected := ""
	scanner := bufio.NewScanner(checksums)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		if filepath.Base(fields[1]) == fileName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("прочитать checksums.txt: %w", err)
	}
	if expected == "" {
		return fmt.Errorf("в checksums.txt нет %s", fileName)
	}

	actual, err := sha256File(filepath.Join(dir, fileName))
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("sha256 %s не совпал: ожидалось %s, получено %s", fileName, expected, actual)
	}

	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("открыть %s: %w", path, err)
	}
	defer file.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", fmt.Errorf("прочитать %s: %w", path, err)
	}

	return hex.EncodeToString(sum.Sum(nil)), nil
}

func binaryVersion(path string) string {
	if _, err := os.Stat(path); err != nil {
		return "none"
	}
	result, err := exec.Command(path, "version").Output()
	if err != nil {
		return "unknown"
	}
	version := strings.TrimSpace(string(result))
	if version == "" {
		return "unknown"
	}

	return version
}

func replaceBinary(src, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0o755); err != nil {
		return fmt.Errorf("записать %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("заменить %s: %w", dst, err)
	}

	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	return os.Chmod(dst, mode)
}

func writeRunnerState(path, previousVersion, previousBin, targetVersion string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	content := fmt.Sprintf(
		"PREVIOUS_VERSION=%s\nPREVIOUS_BIN=%s\nTARGET_VERSION=%s\nUPDATED_AT=%s\n",
		previousVersion,
		previousBin,
		targetVersion,
		time.Now().UTC().Format(time.RFC3339),
	)
	_ = os.WriteFile(path, []byte(content), 0o600)
}

func readRunnerState(path string) (previousVersion, previousBin string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "PREVIOUS_VERSION="):
			previousVersion = strings.TrimPrefix(line, "PREVIOUS_VERSION=")
		case strings.HasPrefix(line, "PREVIOUS_BIN="):
			previousBin = strings.TrimPrefix(line, "PREVIOUS_BIN=")
		}
	}

	return previousVersion, previousBin
}

func confirmUpdate(prompt string, assumeYes bool) error {
	if assumeYes {
		return nil
	}

	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s нет TTY: повторите с --yes", prompt)
	}

	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "д", "да":
		return nil
	}

	return fmt.Errorf("отменено")
}

func restartRunner(out io.Writer, noRestart bool) error {
	if noRestart {
		fmt.Fprintln(out, "Перезапуск пропущен (--no-restart)")
		return nil
	}

	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Fprintln(out, "systemctl не найден: перезапустите раннер вручную")
		return nil
	}

	// --no-block: команда не ждёт остановки текущего процесса, который сам
	// является частью перезапускаемого юнита.
	if err := runCommand("systemctl", "restart", "--no-block", "unitman-runner"); err != nil {
		return fmt.Errorf("systemctl restart unitman-runner: %w", err)
	}
	fmt.Fprintln(out, "Перезапуск unitman-runner запрошен")

	return nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return fmt.Errorf("%w: %s", err, trimmed)
		}
		return err
	}

	return nil
}
