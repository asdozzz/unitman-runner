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
	// Каталог томов внутри юнита: попадает в образ контейнера как /app/unitman_volumes.
	unitmanVolumesDirName = "unitman_volumes"
	// Каталог приложения юнита: Dockerfile копирует ./app в /app.
	unitmanUnitAppDirName = "app"

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

// PrepareVolumes создаёт каталог unitman_volumes в юните, для каждого тома
// создаёт каталог с именем Volume.Dir, скачивает zip-архив из медиатеки
// и распаковывает его содержимое внутрь каталога тома.
func PrepareVolumes(unitPath string, volumes []Volume) ([]model.Step, error) {
	steps := []model.Step{}

	volumesRootPath := filepath.Join(unitPath, unitmanUnitAppDirName, unitmanVolumesDirName)

	err := os.MkdirAll(volumesRootPath, os.ModePerm)
	steps = model.AddStepToSteps(steps, "mkdir "+volumesRootPath, volumesRootPath, err)
	if err != nil {
		return steps, fmt.Errorf("не удалось создать каталог томов %s: %w", volumesRootPath, err)
	}

	for _, volume := range volumes {
		volumePath, errPath := makeVolumePath(volumesRootPath, volume.Dir)
		if errPath != nil {
			steps = model.AddStepToSteps(steps, "volume dir "+volume.Dir, "", errPath)
			return steps, errPath
		}

		downloadUrl := volume.downloadUrl()

		if downloadUrl == "" {
			err = os.MkdirAll(volumePath, os.ModePerm)
			steps = model.AddStepToSteps(steps, "mkdir "+volumePath, "файл к тому не прикреплён", err)
			if err != nil {
				return steps, fmt.Errorf("не удалось создать каталог тома %s: %w", volumePath, err)
			}

			continue
		}

		// Том пересобирается с нуля, чтобы в каталоге не оставались файлы прошлой подготовки.
		err = os.RemoveAll(volumePath)
		if err != nil {
			steps = model.AddStepToSteps(steps, "clean "+volumePath, "", err)
			return steps, fmt.Errorf("не удалось очистить каталог тома %s: %w", volumePath, err)
		}

		err = os.MkdirAll(volumePath, os.ModePerm)
		steps = model.AddStepToSteps(steps, "mkdir "+volumePath, volumePath, err)
		if err != nil {
			return steps, fmt.Errorf("не удалось создать каталог тома %s: %w", volumePath, err)
		}

		archivePath, cleanupArchive, errDownload := downloadVolumeArchive(downloadUrl, volume.archiveName())
		if errDownload != nil {
			steps = model.AddStepToSteps(steps, "download volume "+volume.Name+" from "+downloadUrl, "", errDownload)
			return steps, errDownload
		}

		archiveName := filepath.Base(archivePath)

		steps = model.AddStepToSteps(steps, "download volume "+volume.Name+" from "+downloadUrl, archiveName, nil)

		errUnzip := unzipVolumeArchive(archivePath, volumePath)
		cleanupArchive()

		steps = model.AddStepToSteps(steps, "unzip "+archiveName+" to "+volumePath, "распакован", errUnzip)
		if errUnzip != nil {
			return steps, errUnzip
		}
	}

	return steps, nil
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

	if preparedDir == "" {
		return "", fmt.Errorf("не задан каталог тома")
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
