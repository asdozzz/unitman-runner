package config

import (
	"encoding/json"
	"errors"
	"os"
)

// ParseConfig читает config.json и проверяет обязательные для рантайма
// значения. Старые config.json без новых полей десериализуются без ошибок,
// но для запуска воркера требуются RunnerID и RunnerSecret.
func ParseConfig() (Configuration, error) {
	configuration := Configuration{}
	file, err := os.Open(ConfigPath)
	if err != nil {
		return configuration, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)

	err = decoder.Decode(&configuration)
	if err != nil {
		return configuration, err
	}

	if len(configuration.TemporalHost) == 0 {
		return configuration, errors.New("TemporalHost not defind in config.json")
	}

	if len(configuration.ApiHost) == 0 {
		return configuration, errors.New("ApiHost not defind in config.json")
	}

	if err := configuration.Validate(); err != nil {
		return configuration, err
	}

	return configuration, nil
}
