package unit

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runner/temporal/activity/unit/model"
)

func makeZipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)

	for name, content := range files {
		// Имя с завершающим "/" — запись каталога.
		if strings.HasSuffix(name, "/") {
			_, err := writer.Create(name)
			if err != nil {
				t.Fatalf("create zip dir entry %s: %v", name, err)
			}

			continue
		}

		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}

		_, err = entry.Write([]byte(content))
		if err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}

	err := writer.Close()
	if err != nil {
		t.Fatalf("close zip archive: %v", err)
	}

	return buffer.Bytes()
}

func prepareTestVolumes(t *testing.T, unitPath string, volumes []Volume) []model.Step {
	t.Helper()

	steps, _, err := PrepareVolumes(unitPath, volumes, nil)
	if err != nil {
		t.Fatalf("PrepareVolumes: %v", err)
	}

	return steps
}

func stagingVolumePath(unitPath string, elements ...string) string {
	parts := append([]string{unitPath, "seed", "volumes"}, elements...)

	return filepath.Join(parts...)
}

func TestPrepareVolumesUnpacksArchivesIntoDir(t *testing.T) {
	archive := makeZipArchive(t, map[string]string{
		"dump.sql":        "select 1;",
		"nested/seed.sql": "select 2;",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	unitPath := t.TempDir()
	url := server.URL

	steps := prepareTestVolumes(t, unitPath, []Volume{
		{Dir: "db", Name: "БД", Url: &url},
		{Dir: "web/uploads", Name: "Файлы"},
	})

	if len(steps) == 0 {
		t.Fatalf("ожидались шаги подготовки томов")
	}

	contents, err := os.ReadFile(stagingVolumePath(unitPath, "db", "dump.sql"))
	if err != nil {
		t.Fatalf("файл тома не распакован: %v", err)
	}

	if string(contents) != "select 1;" {
		t.Fatalf("неверное содержимое файла тома: %q", string(contents))
	}

	nestedContents, err := os.ReadFile(stagingVolumePath(unitPath, "db", "nested", "seed.sql"))
	if err != nil {
		t.Fatalf("вложенный файл тома не распакован: %v", err)
	}

	if string(nestedContents) != "select 2;" {
		t.Fatalf("неверное содержимое вложенного файла тома: %q", string(nestedContents))
	}

	// Том без файла — просто объявленный каталог.
	volumeWithoutFile := stagingVolumePath(unitPath, "web", "uploads")
	info, err := os.Stat(volumeWithoutFile)
	if err != nil {
		t.Fatalf("каталог тома без файла не создан: %v", err)
	}

	if !info.IsDir() {
		t.Fatalf("%s должен быть каталогом", volumeWithoutFile)
	}
}

func TestPrepareVolumesRejectsDirOutsideVolumesRoot(t *testing.T) {
	unitPath := t.TempDir()

	_, _, err := PrepareVolumes(unitPath, []Volume{
		{Dir: "../../etc", Name: "Плохой том"},
	}, nil)
	if err == nil {
		t.Fatalf("ожидалась ошибка для каталога тома вне unitman_volumes")
	}
}

func TestPrepareVolumesFollowsMediatekaFileLink(t *testing.T) {
	archive := makeZipArchive(t, map[string]string{
		"dump.sql": "select 3;",
	})

	archiveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer archiveServer.Close()

	// Ендпоинт медиатеки отвечает JSON с подписанной ссылкой на архив.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mediateka/file/file-1" {
			t.Errorf("неожиданный путь запроса: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"url":"` + archiveServer.URL + `/archive.zip"}}`))
	}))
	defer apiServer.Close()

	unitPath := t.TempDir()
	url := apiServer.URL + "/api/mediateka/file/file-1"

	_ = prepareTestVolumes(t, unitPath, []Volume{{Dir: "db", Name: "БД", Url: &url}})

	contents, err := os.ReadFile(stagingVolumePath(unitPath, "db", "dump.sql"))
	if err != nil {
		t.Fatalf("файл тома не распакован: %v", err)
	}

	if string(contents) != "select 3;" {
		t.Fatalf("неверное содержимое файла тома: %q", string(contents))
	}
}

func TestPrepareVolumesDropsArchiveRootDirNamedAfterArchive(t *testing.T) {
	archive := makeZipArchive(t, map[string]string{
		"db/":             "",
		"db/dump.sql":     "select 4;",
		"db/nested/a.sql": "select 5;",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	unitPath := t.TempDir()
	url := server.URL
	fileName := "db.zip"

	_ = prepareTestVolumes(t, unitPath, []Volume{{Dir: "db", Name: "БД", Url: &url, FileName: &fileName}})

	volumePath := stagingVolumePath(unitPath, "db")

	assertVolumeUnpacked(t, volumePath, "db", "select 4;", "select 5;")
}

// Архив назван иначе, чем каталог тома и его корневой каталог: asd.zip с
// содержимым asd/... должен распаковаться в unitman_volumes/public_files/*.
func TestPrepareVolumesDropsSingleRootDirRegardlessOfArchiveName(t *testing.T) {
	archive := makeZipArchive(t, map[string]string{
		"asd/":            "",
		"asd/file.txt":    "hello",
		"asd/sub/deep.go": "package main",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	unitPath := t.TempDir()
	url := server.URL
	fileName := "asd.zip"

	_ = prepareTestVolumes(t, unitPath, []Volume{{Dir: "public_files", Name: "Файлы", Url: &url, FileName: &fileName}})

	volumePath := stagingVolumePath(unitPath, "public_files")

	contents, err := os.ReadFile(filepath.Join(volumePath, "file.txt"))
	if err != nil {
		t.Fatalf("файл тома должен лежать прямо в каталоге тома: %v", err)
	}

	if string(contents) != "hello" {
		t.Fatalf("неверное содержимое файла тома: %q", string(contents))
	}

	deepContents, err := os.ReadFile(filepath.Join(volumePath, "sub", "deep.go"))
	if err != nil {
		t.Fatalf("вложенный файл тома не распакован: %v", err)
	}

	if string(deepContents) != "package main" {
		t.Fatalf("неверное содержимое вложенного файла тома: %q", string(deepContents))
	}

	// Сегмент asd не нужен: каталог тома уже задан свойством Dir.
	nestedVolumeDir := filepath.Join(volumePath, "asd")
	if _, err := os.Stat(nestedVolumeDir); !os.IsNotExist(err) {
		t.Fatalf("каталог с именем архива не должен создаваться: %s", nestedVolumeDir)
	}
}

// Без единого корневого каталога структура архива сохраняется как есть.
func TestPrepareVolumesKeepsStructureWhenNoSingleRootDir(t *testing.T) {
	archive := makeZipArchive(t, map[string]string{
		"data/dump.sql":  "select 6;",
		"other/seed.sql": "select 7;",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	unitPath := t.TempDir()
	url := server.URL
	fileName := "db.zip"

	_ = prepareTestVolumes(t, unitPath, []Volume{{Dir: "db", Name: "БД", Url: &url, FileName: &fileName}})

	volumePath := stagingVolumePath(unitPath, "db")

	contents, err := os.ReadFile(filepath.Join(volumePath, "data", "dump.sql"))
	if err != nil {
		t.Fatalf("файл тома не распакован: %v", err)
	}

	if string(contents) != "select 6;" {
		t.Fatalf("неверное содержимое файла тома: %q", string(contents))
	}

	otherContents, err := os.ReadFile(filepath.Join(volumePath, "other", "seed.sql"))
	if err != nil {
		t.Fatalf("файл тома не распакован: %v", err)
	}

	if string(otherContents) != "select 7;" {
		t.Fatalf("неверное содержимое файла тома: %q", string(otherContents))
	}
}

// Файл в корне архива означает, что общего каталога-обёртки нет.
func TestPrepareVolumesKeepsRootLevelFiles(t *testing.T) {
	archive := makeZipArchive(t, map[string]string{
		"dump.sql":      "select 8;",
		"data/seed.sql": "select 9;",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	unitPath := t.TempDir()
	url := server.URL
	fileName := "asd.zip"

	_ = prepareTestVolumes(t, unitPath, []Volume{{Dir: "public_files", Name: "Файлы", Url: &url, FileName: &fileName}})

	volumePath := stagingVolumePath(unitPath, "public_files")

	contents, err := os.ReadFile(filepath.Join(volumePath, "dump.sql"))
	if err != nil {
		t.Fatalf("файл из корня архива не распакован: %v", err)
	}

	if string(contents) != "select 8;" {
		t.Fatalf("неверное содержимое файла тома: %q", string(contents))
	}

	seedContents, err := os.ReadFile(filepath.Join(volumePath, "data", "seed.sql"))
	if err != nil {
		t.Fatalf("файл тома не распакован: %v", err)
	}

	if string(seedContents) != "select 9;" {
		t.Fatalf("неверное содержимое файла тома: %q", string(seedContents))
	}
}

// assertVolumeUnpacked проверяет, что содержимое архива лежит прямо в каталоге
// тома, без вложенного каталога dropDir.
func assertVolumeUnpacked(t *testing.T, volumePath string, dropDir string, rootContent string, nestedContent string) {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join(volumePath, "dump.sql"))
	if err != nil {
		t.Fatalf("файл тома должен лежать прямо в каталоге тома: %v", err)
	}

	if string(contents) != rootContent {
		t.Fatalf("неверное содержимое файла тома: %q", string(contents))
	}

	nestedContents, err := os.ReadFile(filepath.Join(volumePath, "nested", "a.sql"))
	if err != nil {
		t.Fatalf("вложенный файл тома не распакован: %v", err)
	}

	if string(nestedContents) != nestedContent {
		t.Fatalf("неверное содержимое вложенного файла тома: %q", string(nestedContents))
	}

	droppedDir := filepath.Join(volumePath, dropDir)
	if _, err := os.Stat(droppedDir); !os.IsNotExist(err) {
		t.Fatalf("каталог с именем архива не должен создаваться: %s", droppedDir)
	}
}

func TestDownloadUrlFromFileIdUsesMediatekaEndpoint(t *testing.T) {
	volume := Volume{Dir: "db", Name: "БД"}
	fileId := "file-1"
	volume.FileId = &fileId

	downloadUrl := volume.downloadUrl()

	if !strings.HasSuffix(downloadUrl, "/api/mediateka/file/file-1") {
		t.Fatalf("ожидался ендпоинт медиатеки, получено: %s", downloadUrl)
	}
}

func TestDownloadUrlEmptyWhenNoFile(t *testing.T) {
	volume := Volume{Dir: "db", Name: "БД"}

	if downloadUrl := volume.downloadUrl(); downloadUrl != "" {
		t.Fatalf("для тома без файла ссылка должна быть пустой, получено: %s", downloadUrl)
	}
}

func TestPrepareVolumesRejectsZipSlip(t *testing.T) {
	archive := makeZipArchive(t, map[string]string{
		"../evil.sql": "drop table users;",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	unitPath := t.TempDir()
	url := server.URL

	_, _, err := PrepareVolumes(unitPath, []Volume{{Dir: "db", Name: "БД", Url: &url}}, nil)
	if err == nil {
		t.Fatalf("ожидалась ошибка для выхода за пределы каталога тома")
	}
}

func TestPrepareVolumesSkipsSeededVolume(t *testing.T) {
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write(makeZipArchive(t, map[string]string{"dump.sql": "select 1;"}))
	}))
	defer server.Close()

	unitPath := t.TempDir()
	url := server.URL

	steps, staged, err := PrepareVolumes(unitPath, []Volume{{Dir: "db", Name: "БД", Url: &url}}, func(dir string) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("PrepareVolumes: %v", err)
	}
	if staged != 0 {
		t.Fatalf("ожидалось 0 подготовленных томов, получено %d", staged)
	}
	if requests != 0 {
		t.Fatalf("засеянный том не должен скачиваться, запросов: %d", requests)
	}
	if len(steps) == 0 {
		t.Fatalf("ожидались шаги проверки тома")
	}
}

func TestPrepareVolumesWritesManifest(t *testing.T) {
	unitPath := t.TempDir()

	_, staged, err := PrepareVolumes(unitPath, []Volume{
		{Dir: "db", Name: "БД"},
		{Dir: "web/uploads", Name: "Файлы"},
	}, nil)
	if err != nil {
		t.Fatalf("PrepareVolumes: %v", err)
	}
	if staged != 2 {
		t.Fatalf("ожидалось 2 подготовленных тома, получено %d", staged)
	}

	manifest, err := os.ReadFile(filepath.Join(unitPath, "seed", "volumes", ".manifest"))
	if err != nil {
		t.Fatalf("манифест томов не создан: %v", err)
	}
	if string(manifest) != "db\nweb/uploads\n" {
		t.Fatalf("неверный манифест томов: %q", string(manifest))
	}
}
