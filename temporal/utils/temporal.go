package utils

import (
	"go.temporal.io/sdk/client"
)

func MakeTemporalClient() (client.Client, error) {
	c, err := client.Dial(client.Options{
		HostPort: "localhost:7233",
	})
	if err != nil {
		return nil, err
	}

	return c, nil
}
