package utils

import (
	"go.temporal.io/sdk/client"
)

type Configuration struct {
	TemporalHost string
}

func MakeTemporalClient(config Configuration) (client.Client, error) {
	c, err := client.Dial(client.Options{
		HostPort: config.TemporalHost, //"localhost:7233",
	})
	if err != nil {
		return nil, err
	}

	return c, nil
}
