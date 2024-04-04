package utils

import (
	"go.temporal.io/sdk/client"
)

func MakeTemporalClient() (client.Client, error) {
	c, err := client.Dial(client.Options{})
	if err != nil {
		return nil, err
	}

	return c, nil
}
