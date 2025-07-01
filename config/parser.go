package config

import (
	"encoding/json"
	"errors"
	"os"
)

func ParseConfig() (Configuration, error) {
	configuration := Configuration{}
	file, err := os.Open("config.json")
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

	return configuration, nil
}
