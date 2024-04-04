package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runner/temporal/activity/project/model"
	"time"
)

type RemoveProjectCommand struct {
	ProjectId string
}

type RemoveProjectResult struct {
	Success bool
	Steps   []model.Step
}

func RemoveProjectActivity(ctx context.Context, command RemoveProjectCommand) (*RemoveProjectResult, error) {
	result := &RemoveProjectResult{
		Success: true,
		Steps:   []model.Step{},
	}
	out, err := json.Marshal(command)
	if err != nil {
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "json.Marshal", Response: err.Error(), Success: false, Unixtime: time.Now().Unix()})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "json.Marshal", Response: "success", Success: true, Unixtime: time.Now().Unix()})
	}

	fmt.Println("RemoveProjectCommand:" + string(out))
	filepath := "./projects/" + command.ProjectId

	err = os.RemoveAll(filepath)
	if err != nil {
		fmt.Println(err.Error())
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "Remove " + filepath, Response: err.Error(), Success: false, Unixtime: time.Now().Unix()})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "Remove " + filepath, Response: "success", Success: true, Unixtime: time.Now().Unix()})
	}

	return result, nil
}
