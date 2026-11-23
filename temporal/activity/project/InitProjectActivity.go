package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path"
	"runner/temporal/activity/project/model"
	"runner/temporal/utils"
	"runner/threat"
	"strings"
	"time"
)

// resursnayaProverkaStep — название шага проверки ресурсов сервера.
const resursnayaProverkaStep = "проверить ресурсы сервера"

type InitProjectCommand struct {
	ProjectId      string
	MainBranchName string
	StorageUrl     string
}

type InitProjectResult struct {
	Success bool
	Steps   []model.Step
}

func InitProjectActivity(ctx context.Context, command InitProjectCommand) (*InitProjectResult, error) {
	result := &InitProjectResult{
		Success: true,
		Steps:   []model.Step{},
	}

	// Клонирование проекта — ресурсоёмкая операция (диск и сеть).
	if blocked, message := threat.BlockedMessage(); blocked {
		result.Success = false
		result.Steps = append(result.Steps, model.Step{
			Command:       resursnayaProverkaStep,
			Response:      message,
			Success:       false,
			Unixtime:      time.Now().Unix(),
			UnixtimeStart: time.Now().Unix(),
		})
		return result, nil
	}

	unixtimeStart := time.Now().Unix()
	// StorageUrl содержит токен админа: в лог и в шаги уходит только URL без
	// учётных данных.
	logCommand := command
	if clean, changed := stripStorageURLCredentials(command.StorageUrl); changed {
		logCommand.StorageUrl = clean
	} else if strings.Contains(command.StorageUrl, "@") {
		logCommand.StorageUrl = "<redacted>"
	}

	out, err := json.Marshal(logCommand)
	if err != nil {
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "json.Marshal", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	}

	fmt.Println("InitProjectCommand:" + string(out))
	filepath := "./projects/" + command.ProjectId + "/mainBranch/"
	unixtimeStart = time.Now().Unix()
	err = os.MkdirAll(filepath, os.ModePerm)
	if err != nil {
		log.Println(err)
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "MkdirAll project dir", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "MkdirAll project dir", Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	unixtimeStart = time.Now().Unix()
	dir, err := ioutil.ReadDir(filepath)
	if err != nil {
		log.Println(err)
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "ReadDir project dir", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "ReadDir project dir", Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	for _, d := range dir {
		fmt.Println("clear item:" + path.Join([]string{filepath, d.Name()}...))
		childDir := path.Join([]string{filepath, d.Name()}...)
		unixtimeStart = time.Now().Unix()
		err = os.RemoveAll(childDir)
		if err != nil {
			fmt.Println(err.Error())
			result.Success = false
			result.Steps = append(result.Steps, model.Step{Command: "Remove child item " + childDir, Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
			return result, nil
		} else {
			result.Steps = append(result.Steps, model.Step{Command: "Remove child item " + childDir, Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		}
	}

	unixtimeStart = time.Now().Unix()
	currentPath, err := os.Getwd()
	if err != nil {
		log.Println(err)
		result.Steps = append(result.Steps, model.Step{Command: "Getwd", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	}

	filepath = currentPath + "/" + filepath
	args := []string{"git", "clone", "--progress", command.StorageUrl, "."}
	argsForResponse := []string{"git", "clone", "--progress", "."}
	unixtimeStart = time.Now().Unix()
	msg, err := utils.ExecCommand(filepath, args)

	if err != nil {
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: strings.Join(argsForResponse, " "), Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Success = true
		result.Steps = append(result.Steps, model.Step{Command: strings.Join(argsForResponse, " "), Response: msg, Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	// Seed-репозиторий на хосте раннера не должен хранить токен админа:
	// юниты клонируют из него локально, сетевой origin им не нужен.
	if clean, changed := stripStorageURLCredentials(command.StorageUrl); changed {
		unixtimeStart = time.Now().Unix()
		args := []string{"git", "remote", "set-url", "origin", clean}
		msg, err := utils.ExecCommand(filepath, args)
		result.Steps = append(result.Steps, model.Step{
			Command:       "git remote set-url origin <clean>",
			Response:      msg,
			Success:       err == nil,
			Unixtime:      time.Now().Unix(),
			UnixtimeStart: unixtimeStart,
		})
		if err != nil {
			result.Success = false
			return result, nil
		}
	}

	return result, nil
}

// stripStorageURLCredentials удаляет логин/токен из URL и сообщает, изменился
// ли он. Для не-URL строк возвращает исходное значение.
func stripStorageURLCredentials(raw string) (string, bool) {
	prepared := strings.TrimSpace(raw)
	if prepared == "" {
		return raw, false
	}

	parsed, err := url.Parse(prepared)
	if err != nil || parsed.User == nil {
		return raw, false
	}

	parsed.User = nil
	clean := parsed.String()

	return clean, clean != raw
}
