package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	// ConfigTemplatePath — шаблон, из которого создаётся рабочий config.json.
	ConfigTemplatePath = "config.json.dist"
	// ConfigPath — путь к конфигу, который читает serve.
	ConfigPath = "config.json"

	hostPlaceholder       = "${UNITMAN_MONO_HOST:-localhost}"
	defaultedHostVariable = "${UNITMAN_MONO_HOST}"
)

// RenderConfig подставляет host во все места шаблона, где используется
// UNITMAN_MONO_HOST, сохраняя остальное форматирование файла.
func RenderConfig(template []byte, host string) ([]byte, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "localhost"
	}
	if strings.ContainsAny(host, " \t\r\n\"\\") {
		return nil, fmt.Errorf("host contains invalid characters")
	}

	source := string(template)
	replaced := false
	for _, placeholder := range []string{hostPlaceholder, defaultedHostVariable} {
		if strings.Contains(source, placeholder) {
			source = strings.ReplaceAll(source, placeholder, host)
			replaced = true
		}
	}
	if !replaced {
		return nil, fmt.Errorf("%s does not contain %s", ConfigTemplatePath, hostPlaceholder)
	}

	return []byte(source), nil
}

// CreateConfig читает шаблон и записывает готовый config.json.
func CreateConfig(templatePath, targetPath, host string) error {
	template, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", templatePath, err)
	}

	rendered, err := RenderConfig(template, host)
	if err != nil {
		return err
	}

	if err := os.WriteFile(targetPath, rendered, 0644); err != nil {
		return fmt.Errorf("write %s: %w", targetPath, err)
	}

	return nil
}
