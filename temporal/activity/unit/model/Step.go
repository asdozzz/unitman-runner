package model

import "time"

type Step struct {
	Command  string
	Response string
	Success  bool
	Unixtime int64
}

func AddStepToSteps(Steps []Step, command string, response string, err error) []Step {
	if err != nil {
		Steps = append(Steps, Step{Command: command, Response: err.Error(), Success: false, Unixtime: time.Now().Unix()})
	} else {
		Steps = append(Steps, Step{Command: command, Response: response, Success: true, Unixtime: time.Now().Unix()})
	}

	return Steps
}
