package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"os"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
)

type NachatZapuskUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
	Commands    []string
	Variables   []model.UnitConfigVariable
	Caches      []Cache
}

type ResultatZapuskaUnita struct {
	Success    int
	Steps      []model.Step
	ResponseId string
}

func wrapResultatZapuska(result *ResultatZapuskaUnita) *ResultatZapuskaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps})
	return result
}

func NachatZapuskUnitaActivity(ctx context.Context, command NachatZapuskUnita) (*ResultatZapuskaUnita, error) {
	result := &ResultatZapuskaUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: uuid.New().String(),
	}

	out, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	fmt.Println("ResultatZapuskaUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	filepath = currentPath + "/" + filepath

	args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "podman pod rm pod_" + command.Name + "_" + command.ProjectName + " --force"}
	msg, errCommand := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
	if errCommand != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	for _, commandString := range command.Commands {
		args := []string{"docker", "compose", "exec", "unit", "sh", "-c", commandString}
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatZapuska(result), nil
		}
	}

	/*var caches []Cache
	caches = append(caches, Cache{
		ServiceName: "web",
		Keys:        []string{"composer.lock"},
		Paths:       []string{"vendor"},
	})
	*/
	MakeCache(command.ProjectName, command.Name, command.ProjectId, command.Id, command.Caches)

	result.Success = 1
	return wrapResultatZapuska(result), nil
}
