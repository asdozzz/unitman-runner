package unit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"runner/threat"

	"go.temporal.io/sdk/activity"
)

// Режимы IDE-сайдкара юнита (plan §8).
const (
	RezhimRedaktoraView = "view"
	RezhimRedaktoraEdit = "edit"
)

const (
	// unitEditorDir — служебный каталог редакторской сессии внутри тома /app.
	// Он не попадает в git: каталог создаётся только на время сессии.
	unitEditorDir = "/app/.unitman"
	// unitEditorTokenPath — файл с личным токеном пользователя. В `.git/config`
	// и в URL origin токен не пишется.
	unitEditorTokenPath = unitEditorDir + "/editor-token"
	// unitEditorHelperPath — git credential helper, читающий токен из файла.
	unitEditorHelperPath = unitEditorDir + "/git-credential-unitman"
	// unitEditorSessionPath — маркер активной редакторской сессии.
	unitEditorSessionPath = unitEditorDir + "/editor-session"
	// unitDenyPushHook — pre-push хук, запрещающий push без редакторской сессии.
	unitDenyPushHook = ".git/hooks/pre-push"
	// unitDenyPushMarker — строка, по которой узнаём свой deny-хук.
	unitDenyPushMarker = "unitman deny push"
)

// IzmenitRezhimRedaktoraUnita — payload активности смены режима IDE.
type IzmenitRezhimRedaktoraUnita struct {
	UnitId string
	Mode   string
}

type ResultatIzmeneniyaRezhimaRedaktora struct {
	Success    int
	Steps      []model.Step
	ResponseId string
}

// PodgotovitSessiyuRedaktora — payload активности подготовки сессии редактора.
//
// UserName/UserEmail необязательны: PHP-сторона пока их не передаёт. Раннер
// использует их для git user.name/user.email (решение 15.12-а), если они есть.
type PodgotovitSessiyuRedaktora struct {
	UnitId    string
	UserId    string
	Mode      string
	Token     string
	UserName  string
	UserEmail string
}

type ResultatPodgotovkiSessiiRedaktora struct {
	Success    int
	Steps      []model.Step
	ResponseId string
}

// NormalizeRezhimRedaktora приводит режим к `view`|`edit`. Любое неизвестное
// значение трактуется как безопасный `view` (обратная совместимость).
func NormalizeRezhimRedaktora(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), RezhimRedaktoraEdit) {
		return RezhimRedaktoraEdit
	}
	return RezhimRedaktoraView
}

// FindUnitDirectory ищет каталог юнита по UnitId, когда ProjectId неизвестен
// (команды смены режима и сессии редактора передают только UnitId).
func FindUnitDirectory(unitID string) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("определить рабочий каталог раннера: %w", err)
	}

	return findUnitDirectory(root, unitID)
}

func findUnitDirectory(root, unitID string) (string, error) {
	if !isUnitPathSegment(unitID) {
		return "", fmt.Errorf("некорректный UnitId %q", unitID)
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}

	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("определить корень раскладки юнитов: %w", err)
	}

	projectsDir := filepath.Join(absolute, "projects")
	projects, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", fmt.Errorf("прочитать каталог проектов %s: %w", projectsDir, err)
	}

	for _, project := range projects {
		if !project.IsDir() || !isUnitPathSegment(project.Name()) {
			continue
		}

		candidate := filepath.Join(projectsDir, project.Name(), "units", unitID)
		info, statErr := os.Stat(candidate)
		if statErr == nil && info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("каталог юнита %q не найден в %s", unitID, projectsDir)
}

// ApplyIdeEditorMode готовит compose.yaml к нужному режиму доступа IDE к
// рабочей копии: `view` — том `:ro` и git-индекс в /tmp, `edit` — том `:rw`
// и настоящий .git/index. Файл compose.yaml перегенерируется при подготовке,
// поэтому функция идемпотентна.
func ApplyIdeEditorMode(unitPath, mode string) error {
	if strings.TrimSpace(unitPath) == "" {
		return fmt.Errorf("не задан каталог юнита")
	}

	composePath := filepath.Join(unitPath, "compose.yaml")
	content, err := os.ReadFile(composePath)
	if err != nil {
		return fmt.Errorf("прочитать compose.yaml: %w", err)
	}

	updated := applyIdeEditorModeContent(string(content), NormalizeRezhimRedaktora(mode))
	if updated == string(content) {
		return nil
	}

	if err := os.WriteFile(composePath, []byte(updated), 0644); err != nil {
		return fmt.Errorf("записать compose.yaml: %w", err)
	}

	return nil
}

// applyIdeEditorModeContent — чистовая часть ApplyIdeEditorMode, вынесена для
// тестов без файловой системы.
//
// В режиме edit IDE получает:
//   - том проекта `:rw` и настоящий .git/index;
//   - запуск от root, иначе coder не сможет писать в root-owned том;
//   - UNITMAN_IDE_EDIT_MODE=1 — entrypoint выбирает settings без readonly.
func applyIdeEditorModeContent(content, mode string) string {
	edit := mode == RezhimRedaktoraEdit
	access := "ro"
	if edit {
		access = "rw"
	}

	lines := strings.Split(content, "\n")
	cleaned := make([]string, 0, len(lines)+2)
	inIde := false
	ideIndex := -1
	ideImageIndex := -1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]

		if trimmed == "ide:" && indent == "  " {
			inIde = true
			ideIndex = len(cleaned)
		} else if inIde && trimmed != "" && len(indent) <= 2 {
			inIde = false
		}

		if strings.HasPrefix(trimmed, "- unit_app:/home/coder/project") {
			line = indent + "- unit_app:/home/coder/project:" + access
		}

		if strings.HasPrefix(trimmed, "- GIT_INDEX_FILE=") ||
			strings.HasPrefix(trimmed, "- UNITMAN_IDE_EDIT_MODE=") {
			continue
		}

		// Маркер root-запуска IDE ставим только для edit и всегда убираем в view.
		if inIde && trimmed == `user: "0:0"` {
			continue
		}

		if inIde && trimmed == "image: asdozzz/unitman-ide:latest" {
			ideImageIndex = len(cleaned)
		}

		cleaned = append(cleaned, line)
	}

	if edit {
		userLine := `    user: "0:0"`
		switch {
		case ideImageIndex >= 0:
			cleaned = insertLineAt(cleaned, ideImageIndex+1, userLine)
		case ideIndex >= 0:
			cleaned = insertLineAt(cleaned, ideIndex+1, userLine)
		}
	}

	// GIT_INDEX_FILE нужен только read-only IDE; в edit используется .git/index.
	envLine := ""
	if edit {
		envLine = "- UNITMAN_IDE_EDIT_MODE=1"
	} else {
		envLine = "- GIT_INDEX_FILE=/tmp/git-index"
	}

	homeIndex, homeIndent := findLinePrefix(cleaned, "- HOME=/home/coder")
	if homeIndex >= 0 {
		cleaned = insertLineAt(cleaned, homeIndex, homeIndent+envLine)
	} else {
		cleaned = append(cleaned, "      "+envLine)
	}

	return strings.Join(cleaned, "\n")
}

// insertLineAt вставляет строку в позицию index, не портя исходный срез.
func insertLineAt(lines []string, index int, line string) []string {
	if index < 0 || index > len(lines) {
		index = len(lines)
	}

	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:index]...)
	result = append(result, line)
	result = append(result, lines[index:]...)

	return result
}

// findLinePrefix ищет первую строку с заданным trimmed-префиксом и возвращает
// её индекс и отступ.
func findLinePrefix(lines []string, prefix string) (int, string) {
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return index, line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		}
	}

	return -1, "      "
}

// IdeRecreateCommand пересоздаёт только IDE-сайдкар, не трогая контейнер
// юнита и его тома. Так смена режима не требует полной переподготовки.
func IdeRecreateCommand() []string {
	return []string{"docker", "compose", "up", "-d", "--no-deps", "--force-recreate", ideComposeService}
}

// UnitServiceRunning сообщает, запущен ли контейнер unit в compose-проекте.
func UnitServiceRunning(unitPath string) (bool, error) {
	return unitServiceRunning(unitPath, utils.ExecCommand)
}

func unitServiceRunning(unitPath string, exec func(string, []string) (string, error)) (bool, error) {
	if strings.TrimSpace(unitPath) == "" {
		return false, fmt.Errorf("не задан каталог юнита")
	}
	if exec == nil {
		return false, fmt.Errorf("не задан исполнитель команд")
	}

	out, err := exec(unitPath, []string{"docker", "compose", "ps", "-q", unitComposeService})
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(out) != "", nil
}

// RecreateIde пересоздаёт IDE-сайдкар и ждёт его готовности.
func RecreateIde(unitPath string) error {
	if _, err := utils.ExecCommand(unitPath, IdeRecreateCommand()); err != nil {
		return fmt.Errorf("пересоздать IDE-сайдкар: %w", err)
	}

	return WaitIdeReady(unitPath)
}

// ApplyIdeModeToPreparedUnit применяет режим IDE к уже подготовленному юниту.
// Если контейнеры не запущены, режим сохраняется в compose.yaml и применится
// при следующем запуске (решение 15.11-а).
func ApplyIdeModeToPreparedUnit(unitPath, mode string) (bool, error) {
	if err := ApplyIdeEditorMode(unitPath, mode); err != nil {
		return false, err
	}

	running, err := UnitServiceRunning(unitPath)
	if err != nil {
		return false, err
	}
	if !running {
		return false, nil
	}

	if err := RecreateIde(unitPath); err != nil {
		return true, err
	}

	return true, nil
}

// ─── git-безопасность юнита ─────────────────────────────────────────────────

// PrepareUnitGitSecurity приводит git-состояние юнита к безопасному дефолту:
// убирает учётные данные из origin и восстанавливает deny-хук. Вызывается
// после клонирования/обновления кода.
func PrepareUnitGitSecurity(unitPath string) error {
	if err := StripOriginCredentials(unitPath); err != nil {
		return err
	}

	return ClearEditorSession(unitPath)
}

// StripOriginCredentials удаляет логин/токен из URL origin. Токен админа
// больше не должен оставаться в .git/config после клонирования/обновления.
func StripOriginCredentials(unitPath string) error {
	const script = `set -eu
cd /app
if git remote get-url origin >/dev/null 2>&1; then
    URL=$(git remote get-url origin)
    CLEAN=$(printf '%s' "$URL" | sed -E 's#^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@]*@#\1#')
    if [ "$CLEAN" != "$URL" ]; then
        git remote set-url origin "$CLEAN"
    fi
fi
`
	_, err := ExecInUnitContainer(unitPath, script)
	if err != nil {
		return fmt.Errorf("удалить токен из origin: %w", err)
	}

	return nil
}

// InstallEditorSession настраивает RW-сессию редактора: пишет личный токен в
// служебный файл, включает credential helper и снимает deny-хук. Токен не
// попадает ни в origin, ни в .git/config, ни в логи команд: файл копируется
// в контейнер через `docker compose cp`, а не через argv/env.
func InstallEditorSession(unitPath, userID, token, userName, userEmail string, persistent bool) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("не задан личный токен редактора")
	}
	if strings.TrimSpace(unitPath) == "" {
		return fmt.Errorf("не задан каталог юнита")
	}

	if err := copyTokenIntoUnit(unitPath, token); err != nil {
		return err
	}

	env := []string{
		"UNITMAN_EDITOR_USER=" + userID,
		"UNITMAN_EDITOR_NAME=" + userName,
		"UNITMAN_EDITOR_EMAIL=" + userEmail,
	}
	if persistent {
		env = append(env, "UNITMAN_EDITOR_PERSIST=1")
	}

	const script = `set -eu
umask 077
DIR=/app/.unitman
mkdir -p "$DIR"
chmod 700 "$DIR"
chmod 600 /app/.unitman/editor-token
cat > /app/.unitman/git-credential-unitman <<'UNITMAN_HELPER_EOF'
#!/bin/sh
# unitman personal token credential helper
TOKEN_FILE=/app/.unitman/editor-token
case "$1" in
    get)
        [ -s "$TOKEN_FILE" ] || exit 0
        echo "username=oauth2"
        printf 'password=%s\n' "$(cat "$TOKEN_FILE")"
        ;;
esac
exit 0
UNITMAN_HELPER_EOF
chmod 700 /app/.unitman/git-credential-unitman
cd /app
git config --local --unset-all credential.helper >/dev/null 2>&1 || true
# Пустой helper сбрасывает унаследованный список, затем подключаем свой.
git config --local --add credential.helper ""
git config --local --add credential.helper /app/.unitman/git-credential-unitman
mkdir -p .git/info
if ! grep -qxF '.unitman/' .git/info/exclude 2>/dev/null; then
    echo '.unitman/' >> .git/info/exclude
fi
if [ -n "${UNITMAN_EDITOR_NAME:-}" ]; then
    git config --local user.name "$UNITMAN_EDITOR_NAME"
fi
if [ -n "${UNITMAN_EDITOR_EMAIL:-}" ]; then
    git config --local user.email "$UNITMAN_EDITOR_EMAIL"
fi
mkdir -p .git/hooks
if [ -f .git/hooks/pre-push ] && grep -q "unitman deny push" .git/hooks/pre-push 2>/dev/null; then
    mv .git/hooks/pre-push .git/hooks/pre-push.unitman-deny
fi
if [ "${UNITMAN_EDITOR_PERSIST:-0}" = "1" ]; then
    printf '%s' "${UNITMAN_EDITOR_USER:-}" > /app/.unitman/editor-session
fi
`
	if _, err := ExecInUnitContainerWithOptions(unitPath, script, UnitExecOptions{Env: env}); err != nil {
		return fmt.Errorf("настроить сессию редактора: %w", err)
	}

	return nil
}

// copyTokenIntoUnit переносит токен в том /app без попадания в argv.
func copyTokenIntoUnit(unitPath, token string) error {
	stageDir, err := os.MkdirTemp(unitPath, ".unitman-token-")
	if err != nil {
		return fmt.Errorf("создать staging токена: %w", err)
	}
	defer os.RemoveAll(stageDir)

	tokenFile := filepath.Join(stageDir, "editor-token")
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		return fmt.Errorf("записать staging токена: %w", err)
	}

	if _, err := ExecInUnitContainer(unitPath, "mkdir -p "+unitEditorDir+" && chmod 700 "+unitEditorDir); err != nil {
		return fmt.Errorf("подготовить каталог сессии: %w", err)
	}

	destination := unitComposeService + ":" + unitEditorTokenPath + ".tmp"
	if _, err := utils.ExecCommand(unitPath, []string{"docker", "compose", "cp", tokenFile, destination}); err != nil {
		return fmt.Errorf("скопировать токен в юнит: %w", err)
	}

	if _, err := ExecInUnitContainer(unitPath, "mv "+unitEditorTokenPath+".tmp "+unitEditorTokenPath+" && chmod 600 "+unitEditorTokenPath); err != nil {
		return fmt.Errorf("зафиксировать токен в юните: %w", err)
	}

	return nil
}

// ClearEditorSession удаляет токен и credential helper и возвращает deny-хук.
func ClearEditorSession(unitPath string) error {
	const script = `set -eu
cd /app || exit 0
rm -f /app/.unitman/editor-token /app/.unitman/editor-token.suspended /app/.unitman/editor-session /app/.unitman/git-credential-unitman
rm -f .git/hooks/pre-push.unitman-deny
mkdir -p .git/hooks
cat > .git/hooks/pre-push <<'UNITMAN_DENY_EOF'
#!/bin/sh
# unitman deny push: no active editor session
echo "unit is read-only: git push is forbidden" >&2
exit 1
UNITMAN_DENY_EOF
chmod +x .git/hooks/pre-push
git config --local --unset-all credential.helper >/dev/null 2>&1 || true
`
	if _, err := ExecInUnitContainer(unitPath, script); err != nil {
		return fmt.Errorf("сбросить сессию редактора: %w", err)
	}

	return nil
}

// SuspendEditorSession временно убирает push-креденшелы, сохраняя токен
// активной сессии для последующего восстановления.
func SuspendEditorSession(unitPath string) error {
	const script = `set -eu
cd /app || exit 0
mkdir -p .git/hooks
if [ -f /app/.unitman/editor-token ] && [ ! -f /app/.unitman/editor-token.suspended ]; then
    mv /app/.unitman/editor-token /app/.unitman/editor-token.suspended
fi
cat > .git/hooks/pre-push <<'UNITMAN_DENY_EOF'
#!/bin/sh
# unitman deny push: no active editor session
echo "unit is read-only: git push is forbidden" >&2
exit 1
UNITMAN_DENY_EOF
chmod +x .git/hooks/pre-push
git config --local --unset-all credential.helper >/dev/null 2>&1 || true
`
	if _, err := ExecInUnitContainer(unitPath, script); err != nil {
		return fmt.Errorf("приостановить сессию редактора: %w", err)
	}

	return nil
}

// ResumeEditorSession возвращает ранее приостановленную сессию. Если её не
// было, оставляет deny-хук на месте.
func ResumeEditorSession(unitPath string) error {
	const script = `set -eu
cd /app || exit 0
if [ -f /app/.unitman/editor-token.suspended ]; then
    mv /app/.unitman/editor-token.suspended /app/.unitman/editor-token
fi
if [ -f /app/.unitman/git-credential-unitman ]; then
    git config --local --unset-all credential.helper >/dev/null 2>&1 || true
    git config --local --add credential.helper ""
    git config --local --add credential.helper /app/.unitman/git-credential-unitman
fi
if [ -f /app/.unitman/editor-token ] && [ -f /app/.unitman/editor-session ]; then
    rm -f .git/hooks/pre-push
else
    mkdir -p .git/hooks
    cat > .git/hooks/pre-push <<'UNITMAN_DENY_EOF'
#!/bin/sh
# unitman deny push: no active editor session
echo "unit is read-only: git push is forbidden" >&2
exit 1
UNITMAN_DENY_EOF
    chmod +x .git/hooks/pre-push
fi
`
	if _, err := ExecInUnitContainer(unitPath, script); err != nil {
		return fmt.Errorf("восстановить сессию редактора: %w", err)
	}

	return nil
}

// editorSessionActive сообщает, есть ли в юните активная редакторская сессия.
func editorSessionActive(unitPath string) (bool, error) {
	if strings.TrimSpace(unitPath) == "" {
		return false, nil
	}

	_, err := ExecInUnitContainer(unitPath, "test -f "+unitEditorSessionPath)
	if err != nil {
		return false, nil
	}

	return true, nil
}

// BeginHodGitSession готовит git-креденшелы для одного хода OpenCode.
//
// RW-ход получает личный токен владельца сессии. Read-only ход гарантированно
// не имеет push-креденшелов: активная сессия временно приостанавливается.
// Возвращает cleanup-функцию и фактический readOnly (например, при пустом
// токене RW-ход деградирует до read-only).
func BeginHodGitSession(unitPath string, readOnly bool, token, userName, userEmail string) (func(), bool, error) {
	noop := func() {}

	if strings.TrimSpace(unitPath) == "" {
		return noop, readOnly, nil
	}

	if readOnly || strings.TrimSpace(token) == "" {
		if err := SuspendEditorSession(unitPath); err != nil {
			return noop, true, err
		}
		return func() { _ = ResumeEditorSession(unitPath) }, true, nil
	}

	active, _ := editorSessionActive(unitPath)
	if err := InstallEditorSession(unitPath, "", token, userName, userEmail, active); err != nil {
		_ = SuspendEditorSession(unitPath)
		return func() { _ = ResumeEditorSession(unitPath) }, true, err
	}

	if active {
		// Активная сессия редактора остаётся после хода.
		return noop, false, nil
	}

	return func() { _ = ClearEditorSession(unitPath) }, false, nil
}

// ─── активности ─────────────────────────────────────────────────────────────

// IzmenitRezhimRedaktoraUnitaActivity пересоздаёт IDE-сайдкар с новым режимом
// доступа. Если юнит не запущен, режим сохраняется в compose.yaml.
func IzmenitRezhimRedaktoraUnitaActivity(
	ctx context.Context,
	command IzmenitRezhimRedaktoraUnita,
) (result *ResultatIzmeneniyaRezhimaRedaktora, err error) {
	result = &ResultatIzmeneniyaRezhimaRedaktora{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	// Пересоздание IDE-сайдкара — ресурсоёмкая операция.
	if blocked, message := threat.BlockedMessage(); blocked {
		result.Steps = model.AddStepToSteps(result.Steps, resursnayaProverkaStep, message, fmt.Errorf("%s", message), time.Now().Unix())
		result.Success = 0
		return result, nil
	}

	mode := NormalizeRezhimRedaktora(command.Mode)

	unixtimeStart := time.Now().Unix()
	unitPath, err := FindUnitDirectory(command.UnitId)
	result.Steps = model.AddStepToSteps(result.Steps, "find unit directory", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	err = ApplyIdeEditorMode(unitPath, mode)
	result.Steps = model.AddStepToSteps(result.Steps, "apply ide mode="+mode, "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	running, err := UnitServiceRunning(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "check unit is running", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	if !running {
		result.Steps = model.AddStepToSteps(result.Steps, "recreate ide", "unit is not running; mode will apply on next start", nil, time.Now().Unix())
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	err = RecreateIde(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "recreate ide mode="+mode, "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	return result, nil
}

// PodgotovitSessiyuRedaktoraActivity настраивает RW-доступ и git-креденшелы
// под конкретного пользователя и его личный токен.
func PodgotovitSessiyuRedaktoraActivity(
	ctx context.Context,
	command PodgotovitSessiyuRedaktora,
) (result *ResultatPodgotovkiSessiiRedaktora, err error) {
	result = &ResultatPodgotovkiSessiiRedaktora{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	// Пересоздание IDE-сайдкара — ресурсоёмкая операция.
	if blocked, message := threat.BlockedMessage(); blocked {
		result.Steps = model.AddStepToSteps(result.Steps, resursnayaProverkaStep, message, fmt.Errorf("%s", message), time.Now().Unix())
		result.Success = 0
		return result, nil
	}

	mode := NormalizeRezhimRedaktora(command.Mode)

	unixtimeStart := time.Now().Unix()
	unitPath, err := FindUnitDirectory(command.UnitId)
	result.Steps = model.AddStepToSteps(result.Steps, "find unit directory", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	err = ApplyIdeEditorMode(unitPath, mode)
	result.Steps = model.AddStepToSteps(result.Steps, "apply ide mode="+mode, "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	running, err := UnitServiceRunning(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "check unit is running", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}
	if !running {
		// Контейнер не запущен: сессию настраивать негде, режим применится
		// при следующем запуске/подготовке.
		result.Steps = model.AddStepToSteps(result.Steps, "recreate ide", "unit is not running; mode will apply on next start", nil, time.Now().Unix())
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	err = RecreateIde(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "recreate ide mode="+mode, "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	if mode == RezhimRedaktoraEdit && strings.TrimSpace(command.Token) != "" {
		unixtimeStart = time.Now().Unix()
		err = InstallEditorSession(unitPath, command.UserId, command.Token, command.UserName, command.UserEmail, true)
		result.Steps = model.AddStepToSteps(result.Steps, "configure editor session", "", err, unixtimeStart)
		if err != nil {
			result.Success = 0
			return result, nil
		}

		return result, nil
	}

	// view-сессия или edit-сессия без токена: push-креденшелов быть не должно.
	unixtimeStart = time.Now().Unix()
	err = ClearEditorSession(unitPath)
	message := ""
	if mode == RezhimRedaktoraEdit && strings.TrimSpace(command.Token) == "" {
		message = "personal token is empty; push credentials are not configured"
	}
	result.Steps = model.AddStepToSteps(result.Steps, "clear editor session", message, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	return result, nil
}
