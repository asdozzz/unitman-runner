package main

import (
	"fmt"
	"runner/config"
	"runner/temporal"
)

func main() {

	configuration, err := config.ParseConfig()
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	temporal.Init(configuration)
}
