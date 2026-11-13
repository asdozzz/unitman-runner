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
	unixtimeStart := time.Now().Unix()
	out, err := json.Marshal(command)
	if err != nil {
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "json.Marshal", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "json.Marshal", Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	fmt.Println("RemoveProjectCommand:" + string(out))
	filepathDir := "./projects/" + command.ProjectId

	unixtimeStart = time.Now().Unix()
	err = os.RemoveAll(filepathDir)
	if err != nil {
		fmt.Println(err.Error())
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "Remove " + filepathDir, Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "Remove " + filepathDir, Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	return result, nil
}
