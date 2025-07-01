package utils

import (
	"go.temporal.io/sdk/client"
	"runner/config"
)

func MakeTemporalClient(config config.Configuration) (client.Client, error) {
	c, err := client.Dial(client.Options{
		HostPort: config.TemporalHost, //"localhost:7233",
	})
	if err != nil {
		return nil, err
	}

	return c, nil
}
