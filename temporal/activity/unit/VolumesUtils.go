package unit

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runner/config"
	"runner/temporal/activity/unit/model"
	"strings"
	"time"
)

const (
	// Каталог томов внутри юнита: /app/unitman_volumes.
	unitmanVolumesDirName = "unitman_volumes"
	// Staging-каталог внутри каталога юнита, который монтируется в /seed.
	unitStagingDirName = "seed"
	// Staging-каталог вложений: <unit>/seed/volumes.
	unitStagingVolumesDirName = "volumes"
	// Staging-каталог OpenCode-навыков: <unit>/seed/skills.
	unitStagingSkillsDirName = "skills"

	unitVolumesScriptPath = "/usr/local/bin/unit-volumes-sync"
	unitVolumeMarkersDir  = "/app/.unitman/volumes"

	volumeDownloadTimeout = 10 * time.Minute
	// Сколько раз разрешено перейти по ссылке из ответа медиатеки.
	volumeDownloadMaxRedirects = 3
)

// Volume — том юнита из контракта NachatPodgotovkuUnita.
//
// FileId — идентификатор архива в медиатеке, Url — ссылка на zip-архив:
// либо сразу подписанная ссылка на объект в хранилище, либо адрес ендпоинта
// `api/mediateka/file/{id}`, который возвращает такую ссылку. Ссылка может быть
// относительной — тогда она достраивается до ApiHost (при наличии FileId
// ендпоинт медиатеки собирается из него). FileName — исходное имя архива.
type Volume struct {
	Dir      string
	Name     string
	Url      *string
	FileName *string
	FileId   *string
}

// VolumeSeedChecker сообщает, был ли том уже засеян в юните.
// nil означает, что проверка недоступна и том нужно подготовить.
type VolumeSeedChecker func(dir string) (bool, error)

// PrepareVolumes скачивает и распаковывает вложения в staging-каталог
// <unit>/seed/volumes. В сам юнит они попадают отдельным шагом через
// unit-volumes-sync: это позволяет сохранить изменения пользователя.
//
// Возвращает шаги, количество подготовленных томов и ошибку.
func PrepareVolumes(unitPath string, volumes []Volume, isSeeded VolumeSeedChecker) ([]model.Step, int, error) {
	steps := []model.Step{}

	if len(volumes) == 0 {
		return steps, 0, nil
	}

	stagingRootPath := filepath.Join(unitPath, unitStagingDirName, unitStagingVolumesDirName)

	unixtimeStart := time.Now().Unix()
	err := os.MkdirAll(stagingRootPath, os.ModePerm)
	steps = model.AddStepToSteps(steps, "mkdir "+stagingRootPath, stagingRootPath, err, unixtimeStart)
	if err != nil {
		return steps, 0, fmt.Errorf("не удалось создать staging-каталог томов %s: %w", stagingRootPath, err)
	}

	manifestEntries := []string{}

	for _, volume := range volumes {
		unixtimeStart = time.Now().Unix()

		if strings.ContainsAny(volume.Dir, "\r\n") {
			errDir := fmt.Errorf("каталог тома содержит перевод строки: %q", volume.Dir)
			steps = model.AddStepToSteps(steps, "volume dir "+volume.Dir, "", errDir, unixtimeStart)
			return steps, 0, errDir
		}

		volumePath, errPath := makeVolumePath(stagingRootPath, volume.Dir)
		if errPath != nil {
			steps = model.AddStepToSteps(steps, "volume dir "+volume.Dir, "", errPath, unixtimeStart)
			return steps, 0, errPath
		}

		if isSeeded != nil {
			seeded, errSeeded := isSeeded(volume.Dir)
			if errSeeded != nil {
				steps = model.AddStepToSteps(steps, "check volume "+volume.Dir, "", errSeeded, unixtimeStart)
				return steps, 0, errSeeded
			}

			if seeded {
				steps = model.AddStepToSteps(steps, "check volume "+volume.Dir, "том уже засеян", nil, unixtimeStart)
				continue
			}
		}

		// Staging пересоздаётся, чтобы не оставались файлы прошлой подготовки.
		unixtimeStart = time.Now().Unix()
		err = os.RemoveAll(volumePath)
		if err != nil {
			steps = model.AddStepToSteps(steps, "clean "+volumePath, "", err, unixtimeStart)
			return steps, 0, fmt.Errorf("не удалось очистить staging-каталог тома %s: %w", volumePath, err)
		}

		unixtimeStart = time.Now().Unix()
		err = os.MkdirAll(volumePath, os.ModePerm)
		steps = model.AddStepToSteps(steps, "mkdir "+volumePath, volumePath, err, unixtimeStart)
		if err != nil {
			return steps, 0, fmt.Errorf("не удалось создать staging-каталог тома %s: %w", volumePath, err)
		}

		downloadUrl := volume.downloadUrl()

		if downloadUrl != "" {
			unixtimeStart = time.Now().Unix()
			archivePath, cleanupArchive, errDownload := downloadVolumeArchive(downloadUrl, volume.archiveName())
			if errDownload != nil {
				steps = model.AddStepToSteps(steps, "download volume "+volume.Name, "", errDownload, unixtimeStart)
				return steps, 0, errDownload
			}

			archiveName := filepath.Base(archivePath)

			steps = model.AddStepToSteps(steps, "download volume "+volume.Name, archiveName, nil, unixtimeStart)

			unixtimeStart = time.Now().Unix()
			errUnzip := unzipVolumeArchive(archivePath, volumePath)
			cleanupArchive()

			steps = model.AddStepToSteps(steps, "unzip "+archiveName+" to "+volumePath, "распакован", errUnzip, unixtimeStart)
			if errUnzip != nil {
				return steps, 0, errUnzip
			}
		} else {
			steps = model.AddStepToSteps(steps, "volume "+volume.Dir, "файл к тому не прикреплён", nil, unixtimeStart)
		}

		manifestEntries = append(manifestEntries, volume.Dir)
	}

	manifestPath := filepath.Join(stagingRootPath, ".manifest")
	manifestContent := ""
	if len(manifestEntries) > 0 {
		manifestContent = strings.Join(manifestEntries, "\n") + "\n"
	}

	unixtimeStart = time.Now().Unix()
	err = os.WriteFile(manifestPath, []byte(manifestContent), 0644)
	steps = model.AddStepToSteps(steps, "write volume manifest", manifestPath, err, unixtimeStart)
	if err != nil {
		return steps, 0, fmt.Errorf("не удалось записать манифест томов %s: %w", manifestPath, err)
	}

	return steps, len(manifestEntries), nil
}

// UnitVolumeSeeded проверяет маркер тома внутри юнита.
func UnitVolumeSeeded(unitPath, dir string) (bool, error) {
	markerPath := unitVolumeMarkersDir + "/" + strings.TrimPrefix(dir, "/")

	_, err := ExecInUnitContainer(unitPath, "test -e "+shellQuote(markerPath))
	if err == nil {
		return true, nil
	}

	message := strings.ToLower(err.Error())
	if strings.Contains(message, "is not running") || strings.Contains(message, "no such service") {
		return false, err
	}

	if strings.Contains(message, "exit status 1") {
		return false, nil
	}

	return false, err
}

// SyncUnitVolumes копирует подготовленный staging в /app/unitman_volumes.
func SyncUnitVolumes(unitPath string) (string, error) {
	return ExecInUnitContainer(unitPath, unitVolumesScriptPath)
}

// shellQuote экранирует значение для sh -c.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// downloadUrl возвращает ссылку на архив тома.
//
// Если ссылки нет, но известен идентификатор файла, используется ендпоинт
// медиатеки `api/mediateka/file/{id}`.
func (volume Volume) downloadUrl() string {
	if volume.Url != nil {
		preparedUrl := strings.TrimSpace(*volume.Url)

		if preparedUrl != "" {
			return resolveApiHostUrl(preparedUrl)
		}
	}

	if volume.FileId != nil {
		fileId := strings.TrimSpace(*volume.FileId)

		if fileId != "" {
			return resolveApiHostUrl("/api/mediateka/file/" + url.PathEscape(fileId))
		}
	}

	return ""
}

// archiveName возвращает имя zip-архива тома.
func (volume Volume) archiveName() string {
	if volume.FileName != nil {
		rawName := strings.TrimSpace(*volume.FileName)

		if rawName != "" {
			return filepath.Base(rawName)
		}
	}

	if volume.Url == nil {
		return ""
	}

	return archiveNameFromUrl(*volume.Url)
}

// archiveNameFromUrl достаёт имя архива из ссылки на скачивание.
func archiveNameFromUrl(downloadUrl string) string {
	parsedUrl, err := url.Parse(downloadUrl)
	if err != nil {
		return ""
	}

	archiveName := path.Base(parsedUrl.Path)

	if !strings.HasSuffix(strings.ToLower(archiveName), ".zip") {
		return ""
	}

	return archiveName
}

// resolveApiHostUrl достраивает относительную ссылку до ApiHost.
func resolveApiHostUrl(downloadUrl string) string {
	if !strings.HasPrefix(downloadUrl, "/") {
		return downloadUrl
	}

	configuration, err := config.ParseConfig()
	if err != nil {
		return downloadUrl
	}

	return strings.TrimRight(configuration.ApiHost, "/") + downloadUrl
}

// makeVolumePath собирает путь каталога тома и не даёт выйти за пределы каталога томов.
func makeVolumePath(volumesRootPath string, dir string) (string, error) {
	preparedDir := strings.TrimSpace(dir)

	if preparedDir == "" || preparedDir == "." || preparedDir == ".." {
		return "", fmt.Errorf("некорректный каталог тома: %q", dir)
	}

	volumePath := filepath.Join(volumesRootPath, filepath.FromSlash(preparedDir))

	relativePath, err := filepath.Rel(volumesRootPath, volumePath)
	if err != nil {
		return "", err
	}

	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("каталог тома выходит за пределы %s: %s", unitmanVolumesDirName, dir)
	}

	return volumePath, nil
}

// mediatekaFileResponse — ответ ендпоинта `api/mediateka/file/{id}`.
type mediatekaFileResponse struct {
	Status string `json:"status"`
	Data   struct {
		Url string `json:"url"`
	} `json:"data"`
}

// downloadVolumeArchive скачивает zip-архив тома во временный каталог.
//
// Возвращает путь к архиву и функцию очистки временного каталога.
// Если по ссылке отвечает ендпоинт медиатеки (JSON с подписанной ссылкой),
// переход по этой ссылке выполняется автоматически.
func downloadVolumeArchive(downloadUrl string, wantedArchiveName string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "unitman_volume_*")
	if err != nil {
		return "", nil, err
	}

	cleanup := func() {
		removeErr := os.RemoveAll(tempDir)
		if removeErr != nil {
			fmt.Println("error remove volume temp dir " + tempDir + ": " + removeErr.Error())
		}
	}

	requestUrl := downloadUrl

	for attempt := 0; attempt <= volumeDownloadMaxRedirects; attempt++ {
		responsePath, nextUrl, err := fetchVolumeResponse(tempDir, requestUrl)
		if err != nil {
			cleanup()
			return "", nil, err
		}

		if nextUrl != "" {
			removeErr := os.Remove(responsePath)
			if removeErr != nil {
				fmt.Println("error remove volume response " + responsePath + ": " + removeErr.Error())
			}

			requestUrl = resolveApiHostUrl(nextUrl)

			continue
		}

		// Имя задаётся по контракту (FileName), иначе берётся из итоговой ссылки.
		archiveName := filepath.Base(wantedArchiveName)

		if wantedArchiveName == "" {
			archiveName = archiveNameFromUrl(requestUrl)
		}

		if archiveName == "" {
			return responsePath, cleanup, nil
		}

		archivePath := filepath.Join(tempDir, archiveName)

		err = os.Rename(responsePath, archivePath)
		if err != nil {
			cleanup()
			return "", nil, err
		}

		return archivePath, cleanup, nil
	}

	cleanup()

	return "", nil, fmt.Errorf("не удалось получить архив тома %s: превышено число переходов по ссылкам", downloadUrl)
}

// fetchVolumeResponse скачивает один ответ по ссылке.
//
// Возвращает путь к файлу с телом ответа и ссылку для повторного запроса,
// если ответом оказался JSON медиатеки со ссылкой на архив.
func fetchVolumeResponse(tempDir string, requestUrl string) (string, string, error) {
	client := http.Client{Timeout: volumeDownloadTimeout}

	res, err := client.Get(requestUrl)
	if err != nil {
		return "", "", fmt.Errorf("не удалось скачать архив тома: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("не удалось скачать архив тома %s: %s", requestUrl, res.Status)
	}

	responseFile, err := os.CreateTemp(tempDir, "response_*")
	if err != nil {
		return "", "", err
	}

	responsePath := responseFile.Name()

	_, err = io.Copy(responseFile, res.Body)
	if err != nil {
		responseFile.Close()
		os.Remove(responsePath)
		return "", "", fmt.Errorf("не удалось сохранить ответ по ссылке тома: %w", err)
	}

	err = responseFile.Close()
	if err != nil {
		os.Remove(responsePath)
		return "", "", err
	}

	if !strings.Contains(res.Header.Get("Content-Type"), "json") {
		return responsePath, "", nil
	}

	nextUrl, err := parseMediatekaFileUrl(responsePath)

	if err != nil {
		os.Remove(responsePath)
		return "", "", err
	}

	return responsePath, nextUrl, nil
}

// parseMediatekaFileUrl достаёт подписанную ссылку из ответа ендпоинта медиатеки.
func parseMediatekaFileUrl(responsePath string) (string, error) {
	body, err := os.ReadFile(responsePath)
	if err != nil {
		return "", err
	}

	response := mediatekaFileResponse{}

	err = json.Unmarshal(body, &response)
	if err != nil {
		return "", fmt.Errorf("не удалось разобрать ответ медиатеки: %w", err)
	}

	if response.Status != "success" {
		return "", fmt.Errorf("медиатека вернула ошибку по файлу тома: %s", string(body))
	}

	if strings.TrimSpace(response.Data.Url) == "" {
		return "", fmt.Errorf("медиатека не вернула ссылку на архив тома")
	}

	return strings.TrimSpace(response.Data.Url), nil
}

// unzipVolumeArchive распаковывает содержимое zip-архива в каталог тома.
//
// Каталог с именем архива внутри тома не создаётся: если все записи архива
// лежат в единственном каталоге верхнего уровня (так получается, когда
// архивируют папку целиком: asd.zip → asd/...), этот уровень отбрасывается.
// Каталог тома уже задан свойством Dir, поэтому лишний сегмент не нужен.
func unzipVolumeArchive(archivePath string, destinationPath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("архив тома не является zip: %w", err)
	}
	defer reader.Close()

	rootDirName := archiveRootDirName(reader.File)

	for _, archiveFile := range reader.File {
		entryName := archiveEntryName(archiveFile.Name)

		// Каталоги в zip могут быть записаны как с завершающим "/", так и без него.
		isDir := archiveFile.FileInfo().IsDir() || strings.HasSuffix(entryName, "/")

		entryName = strings.TrimSuffix(entryName, "/")

		if rootDirName != "" {
			// Запись самого корневого каталога архива пропускается вместе с уровнем.
			if entryName == rootDirName {
				continue
			}

			entryName = strings.TrimPrefix(entryName, rootDirName+"/")
		}

		if entryName == "" {
			continue
		}

		targetPath, errTargetPath := makeArchiveTargetPath(destinationPath, entryName)
		if errTargetPath != nil {
			return errTargetPath
		}

		if isDir {
			err = os.MkdirAll(targetPath, os.ModePerm)
			if err != nil {
				return err
			}

			continue
		}

		// Симлинки из архива не создаём: они могут указывать за пределы каталога тома.
		if archiveFile.Mode()&os.ModeSymlink != 0 {
			continue
		}

		err = os.MkdirAll(filepath.Dir(targetPath), os.ModePerm)
		if err != nil {
			return err
		}

		err = extractVolumeArchiveFile(archiveFile, targetPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// archiveEntryName приводит имя записи архива к виду с разделителями "/".
//
// В zip разделителем каталогов обязан быть "/", но архивы, собранные в
// Windows, иногда содержат "\", поэтому оба варианта приводятся к "/".
func archiveEntryName(archiveName string) string {
	preparedName := strings.ReplaceAll(archiveName, "\\", "/")

	for strings.HasPrefix(preparedName, "./") {
		preparedName = strings.TrimPrefix(preparedName, "./")
	}

	return preparedName
}

// archiveRootDirName возвращает имя единственного каталога верхнего уровня
// архива, внутри которого лежат все его записи.
//
// Такой каталог появляется, когда архивируют папку целиком (asd.zip →
// asd/...). При распаковке он отбрасывается, потому что каталог тома уже
// задан свойством Volume.Dir. Если в корне архива есть файлы или несколько
// разных каталогов, общего уровня нет и структура архива сохраняется как есть.
func archiveRootDirName(files []*zip.File) string {
	rootDirName := ""

	for _, archiveFile := range files {
		rawEntryName := archiveEntryName(archiveFile.Name)
		entryName := strings.TrimSuffix(rawEntryName, "/")

		if entryName == "" {
			continue
		}

		parts := strings.SplitN(entryName, "/", 2)
		topLevelName := parts[0]

		// Имена вида ".." или "." — это попытка выйти за пределы каталога тома,
		// а не каталог-обёртка: такой архив обрабатывается как есть, а запись
		// отклонит проверка пути распаковки.
		if !isArchiveDirName(topLevelName) {
			return ""
		}

		isDirEntry := archiveFile.FileInfo().IsDir() || strings.HasSuffix(rawEntryName, "/")

		if len(parts) == 1 && !isDirEntry {
			// Файл в корне архива — общего каталога-обёртки нет.
			return ""
		}

		if rootDirName == "" {
			rootDirName = topLevelName
			continue
		}

		if topLevelName != rootDirName {
			return ""
		}
	}

	return rootDirName
}

// isArchiveDirName сообщает, годится ли сегмент пути как имя каталога-обёртки.
func isArchiveDirName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}

	return !strings.ContainsAny(name, `/\:`)
}

// makeArchiveTargetPath не даёт архиву распаковаться за пределы каталога тома.
func makeArchiveTargetPath(destinationPath string, entryName string) (string, error) {
	targetPath := filepath.Join(destinationPath, filepath.FromSlash(entryName))

	relativePath, err := filepath.Rel(destinationPath, targetPath)
	if err != nil {
		return "", err
	}

	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("некорректный путь в архиве тома: %s", entryName)
	}

	return targetPath, nil
}

func extractVolumeArchiveFile(archiveFile *zip.File, targetPath string) error {
	mode := archiveFile.Mode().Perm()
	if mode == 0 {
		mode = 0644
	}

	source, err := archiveFile.Open()
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	_, err = io.Copy(target, source)

	closeErr := target.Close()
	if err != nil {
		return err
	}

	return closeErr
}
