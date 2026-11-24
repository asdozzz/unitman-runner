package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Configuration — рантайм-конфигурация раннера.
//
// RunnerID, RunnerSecret, RegistryHost и адреса сервисов заполняются командой
// `runner init` после успешной регистрации на backend. Пустые значения
// допустимы только при чтении старого config.json; для запуска воркера
// RunnerID и RunnerSecret обязательны.
type Configuration struct {
	// TemporalHost — host:port Temporal, собранный из host и ports.temporal.
	TemporalHost string `json:"TemporalHost"`
	// ApiHost — базовый URL backend API (scheme://host:ports.api).
	ApiHost string `json:"ApiHost"`

	// RunnerID — UUID v4, выданный backend-ом при регистрации. Он же
	// используется как Temporal task queue: отдельная очередь не хранится.
	RunnerID string `json:"runnerId"`
	// RunnerSecret — per-runner секрет HMAC-SHA256 для callback-подписей.
	// Всегда 64 hex-символа; никогда не берётся из окружения.
	RunnerSecret string `json:"runnerSecret"`
	// RegistryHost — Docker-ссылка реестра host:ports.registry без схемы
	// (image reference не допускает scheme).
	RegistryHost string `json:"registryHost"`
}

// Sign возвращает нижний регистр hex(HMAC-SHA256(payload, RunnerSecret)).
func (c Configuration) Sign(payload []byte) string {
	return Sign(c.RunnerSecret, payload)
}

// Sign возвращает нижний регистр hex(HMAC-SHA256(payload, secret)).
//
// Подписывается ровно переданный срез байт: вызывающая сторона обязана
// сериализовать тело один раз и отправлять те же байты.
func Sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	// hash.Hash.Write для sha256 не возвращает ошибку; контракт io.Writer.
	_, _ = mac.Write(payload)

	return hex.EncodeToString(mac.Sum(nil))
}

// Validate проверяет обязательные для рантайма поля.
func (c Configuration) Validate() error {
	if strings.TrimSpace(c.RunnerID) == "" {
		return errors.New("RunnerID не задан в config.json (выполните runner init)")
	}
	if strings.TrimSpace(c.RunnerSecret) == "" {
		return errors.New("RunnerSecret не задан в config.json (выполните runner init)")
	}
	if len(c.RunnerSecret) != 64 {
		return errors.New("RunnerSecret в config.json должен быть 64 hex-символа")
	}
	if _, err := hex.DecodeString(c.RunnerSecret); err != nil {
		return errors.New("RunnerSecret в config.json должен быть hex-строкой")
	}

	return nil
}
