package unit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runner/config"
	"runner/temporal/activity/unit/model"
	"time"
)

type SaveStepsCommand struct {
	ResponseId string        `json:"responseId"`
	Steps      *[]model.Step `json:"stepsContent"`
}

type SaveStepsRequest struct {
	ResponseId   string `json:"responseId"`
	StepsContent string `json:"stepsContent"`
}

type SaveStepsResponse struct {
	Id string `json:"id"`
}

func SaveUnitSteps(command SaveStepsCommand) {
	configuration, err := config.ParseConfig()

	if err != nil {
		fmt.Println("----------------------", err)
		return
	}

	marshalled, err := json.Marshal(command.Steps)

	if err != nil {
		fmt.Println("----------------------", err)
		return
	}

	request := SaveStepsRequest{ResponseId: command.ResponseId, StepsContent: string(marshalled)}

	requestMarshalled, err := json.Marshal(request)

	if err != nil {
		fmt.Println("----------------------", err)
		return
	}

	req, err := http.NewRequest("POST", configuration.ApiHost+"/api/runner/saveSteps", bytes.NewReader(requestMarshalled))
	if err != nil {
		fmt.Println("----------------------", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		fmt.Println("----------------------", err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			log.Println("SSSSSSSSSSSSSSSSSSSSSSSSS", res.StatusCode, err)
		}
		fmt.Println("SSSSSSSSSSSSSSSSSSSSSSSSS", res.StatusCode, string(bodyBytes))
		return
	}

	*command.Steps = []model.Step{}

	return
}
