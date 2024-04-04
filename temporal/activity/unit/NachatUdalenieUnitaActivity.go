package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"runner/temporal/activity/unit/model"
)

type NachatUdalenieUnita struct {
	ProjectId string
	Name      string
}

type ResultatUdaleniyaUnita struct {
	Success int
	Steps   []model.Step
}

func NachatUdalenieUnitaActivity(ctx context.Context, command NachatUdalenieUnita) (*ResultatUdaleniyaUnita, error) {
	result := &ResultatUdaleniyaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("NachatUdalenieUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Name
	dir, err := ioutil.ReadDir(filepath)
	result.Steps = model.AddStepToSteps(result.Steps, "ReadDir", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}
	for _, d := range dir {
		item := path.Join([]string{filepath, d.Name()}...)
		err = os.RemoveAll(item)
		result.Steps = model.AddStepToSteps(result.Steps, "remove "+item, "success", err)
		if err != nil {
			result.Success = 0
			return result, nil
		}
	}

	result.Success = 1
	return result, nil
}
