package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runner/temporal"
	"runner/temporal/utils"
)

func main() {

	file, err := os.Open("config.json")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	configuration := utils.Configuration{}
	err = decoder.Decode(&configuration)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	temporal.Init(configuration)
}
