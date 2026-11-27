// Package unitvariables расшифровывает hidden-переменные юнита, которые
// backend передаёт в Temporal payload только в виде шифротекста.
//
// Ключ детерминированно выводится из per-runner секрета (config.json):
//
//	PRK = HMAC-SHA256(salt=empty, runnerSecret)
//	key = HMAC-SHA256(PRK, "unitman-unit-variables-v1" || 0x01)[:32]
//
// Это совпадает с PHP hash_hkdf('sha256', runnerSecret, 32, info) на backend.
// Plaintext никогда не логируется и не сериализуется обратно в Temporal.
package unitvariables

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"runner/config"
	"runner/temporal/activity/unit/model"
)

const hkdfInfo = "unitman-unit-variables-v1"

// DeriveKey выводит 32-байтовый AES-ключ из per-runner секрета.
func DeriveKey(runnerSecret string) []byte {
	// HKDF-Extract: пустая соль эквивалентна HMAC с zero-filled ключом.
	extract := hmac.New(sha256.New, nil)
	extract.Write([]byte(runnerSecret))
	prk := extract.Sum(nil)

	// HKDF-Expand, один блок: T(1) = HMAC(PRK, info || 0x01).
	expand := hmac.New(sha256.New, prk)
	expand.Write([]byte(hkdfInfo))
	expand.Write([]byte{0x01})

	return expand.Sum(nil)[:32]
}

// Decrypt расшифровывает base64(nonce||tag||ciphertext) с AAD.
func Decrypt(ciphertext, aad, runnerSecret string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil || len(raw) < 12+16 {
		return "", errors.New("unitvariables: invalid ciphertext")
	}

	nonce := raw[:12]
	tag := raw[12:28]
	payload := raw[28:]

	block, err := aes.NewCipher(DeriveKey(runnerSecret))
	if err != nil {
		return "", errors.New("unitvariables: invalid key")
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", errors.New("unitvariables: invalid gcm")
	}

	sealed := make([]byte, 0, len(payload)+len(tag))
	sealed = append(sealed, payload...)
	sealed = append(sealed, tag...)

	plain, err := gcm.Open(nil, nonce, sealed, []byte(aad))
	if err != nil {
		return "", errors.New("unitvariables: decrypt failed")
	}

	return string(plain), nil
}

// Resolve возвращает копию variables, где hidden-значения расшифрованы.
// Для не-hidden переменных возвращаются исходные значения.
func Resolve(variables []model.UnitConfigVariable, unitID, runnerSecret string) ([]model.UnitConfigVariable, error) {
	resolved := make([]model.UnitConfigVariable, len(variables))
	copy(resolved, variables)

	for i := range resolved {
		if !resolved[i].Hidden {
			continue
		}

		if resolved[i].ValueCipher == "" {
			return nil, errors.New("unitvariables: hidden variable has no cipher")
		}

		plain, err := Decrypt(
			resolved[i].ValueCipher,
			unitID+":"+resolved[i].Id,
			runnerSecret,
		)
		if err != nil {
			return nil, err
		}

		resolved[i].Value = plain
		resolved[i].ValueCipher = ""
	}

	return resolved, nil
}

// ResolveFromConfig читает RunnerSecret из config.json только если среди
// переменных есть hidden. Иначе возвращает исходный срез без чтения конфига.
func ResolveFromConfig(variables []model.UnitConfigVariable, unitID string) ([]model.UnitConfigVariable, error) {
	for _, variable := range variables {
		if !variable.Hidden {
			continue
		}

		configuration, err := config.ParseConfig()
		if err != nil {
			return nil, err
		}

		return Resolve(variables, unitID, configuration.RunnerSecret)
	}

	return variables, nil
}
