package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Встроенные шаблоны и конфиги раннера. Бинарь самодостаточен: при старте
// содержимое распаковывается в рабочий каталог (UNITMAN_HOME или текущий),
// поэтому установка не требует дерева репозитория.
//
//go:embed temporal/activity/unit/docker-dind temporal/activity/workspace/docker-dind all:.docker/proxy
var embeddedAssets embed.FS

// enterHome переходит в UNITMAN_HOME, если переменная задана. Каталог
// создаётся при необходимости. config.json и projects/ живут в этом каталоге.
func enterHome() error {
	home := strings.TrimSpace(os.Getenv("UNITMAN_HOME"))
	if home == "" {
		return nil
	}

	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("создать UNITMAN_HOME %s: %w", home, err)
	}

	if err := os.Chdir(home); err != nil {
		return fmt.Errorf("перейти в UNITMAN_HOME %s: %w", home, err)
	}

	return nil
}

// extractEmbeddedAssets раскладывает встроенные файлы в текущий каталог.
// Существующие файлы перезаписываются только при расхождении содержимого.
func extractEmbeddedAssets() error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("определить рабочий каталог: %w", err)
	}

	return fs.WalkDir(embeddedAssets, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		target := filepath.Join(root, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, readErr := embeddedAssets.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		if existing, errRead := os.ReadFile(target); errRead == nil && bytes.Equal(existing, data) {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		mode := os.FileMode(0o644)
		if strings.HasSuffix(entry.Name(), ".sh") {
			mode = 0o755
		}

		return os.WriteFile(target, data, mode)
	})
}
