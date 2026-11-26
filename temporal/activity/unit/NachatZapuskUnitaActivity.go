package unit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runner/state"
	"runner/temporal/activity/unit/model"
	"runner/threat"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

type NachatZapuskUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
	Commands    []string
	Variables   []model.UnitConfigVariable
	Branch      string
}

type ResultatZapuskaUnita struct {
	Success    int
	Steps      []model.Step
	ResponseId string
	//Ports      []model.UnitPort
}

func wrapZapuskaToMono(result *ResultatZapuskaUnita) {
	SaveStepsToMono(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
}

func wrapResultatZapuska(result *ResultatZapuskaUnita) *ResultatZapuskaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
	return result
}

func wrapResultatZapuskaSuccess(result *ResultatZapuskaUnita) *ResultatZapuskaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: true})
	return result
}

func NachatZapuskUnitaActivity(ctx context.Context, command NachatZapuskUnita) (result *ResultatZapuskaUnita, err error) {
	result = &ResultatZapuskaUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("zapusk", command.ProjectId, command.Id, command.Name, command.ProjectName, success)
	}()

	// Ресурсоёмкая активность: при уровне блокировки не запускаем юнит.
	if blocked, message := threat.BlockedMessage(); blocked {
		result.Steps = model.AddStepToSteps(result.Steps, resursnayaProverkaStep, message, errors.New(message), time.Now().Unix())
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	_, err = json.Marshal(command)
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	// Имена юнита и проекта для compose-интерполяции берутся из .env рабочего
	// каталога: мутация окружения процесса протекала между активностями
	// и разворачивала контейнеры с чужими именами.
	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	filepath = currentPath + "/" + filepath

	for _, commandString := range command.Commands {
		unixtimeStart := time.Now().Unix()
		args := UnitContainerCommand(commandString)
		msg, errCommand := ExecInUnitContainer(filepath, commandString)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand, unixtimeStart)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatZapuska(result), nil
		} else {
			wrapZapuskaToMono(result)
		}
	}
	/*
		unixtimeStart := time.Now().Unix()
		ports, errPorts := CollectUnitPorts(filepath)
		if errPorts != nil {
			result.Steps = model.AddStepToSteps(result.Steps, unitPortsStepCommand, "", errPorts, unixtimeStart)
		} else {
			result.Ports = ports
			summary := fmt.Sprintf("Собрано портов: %d", len(ports))
			result.Steps = model.AddStepToSteps(result.Steps, unitPortsStepCommand, summary, nil, unixtimeStart)

			httpProbeStart := time.Now().Unix()
			httpPorts, errHTTP := DetectHTTPPorts(filepath, command.Name, command.ProjectName, ports)
			if errHTTP != nil {
				// Ошибка проверки не влияет на успех запуска: порты остаются как есть.
				result.Steps = model.AddStepToSteps(result.Steps, unitHTTPProbeStepCommand, "", errHTTP, httpProbeStart)
			} else {
				result.Ports = httpPorts
				result.Steps = model.AddStepToSteps(result.Steps, unitHTTPProbeStepCommand, httpPortsSummary(httpPorts), nil, httpProbeStart)
			}
		}
	*/

	result.Success = 1
	return wrapResultatZapuskaSuccess(result), nil
}
