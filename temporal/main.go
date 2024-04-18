package temporal

import (
	"go.temporal.io/sdk/worker"
	"log"
	projectActivity "runner/temporal/activity/project"
	"runner/temporal/activity/runner"
	unitActivity "runner/temporal/activity/unit"
	"runner/temporal/utils"
)

func Init() {
	// Create the client object just once per process
	c, err := utils.MakeTemporalClient()

	if err != nil {
		log.Fatalln("unable to connect to temporal", err)
		return
	}

	defer c.Close()

	// This worker hosts both Workflow and Activity functions
	w := worker.New(c, "unitman-runner-queue", worker.Options{})
	w.RegisterActivity(projectActivity.InitProjectActivity)
	w.RegisterActivity(projectActivity.RemoveProjectActivity)
	w.RegisterActivity(unitActivity.NachatSborkuUnitaActivity)
	w.RegisterActivity(unitActivity.NachatUdalenieUnitaActivity)
	w.RegisterActivity(unitActivity.NachatObnovlenieUnitaActivity)
	w.RegisterActivity(unitActivity.NachatPodgotovkuUnitaActivity)
	w.RegisterActivity(unitActivity.NachatSbrosPodgotovkiUnitaActivity)
	w.RegisterActivity(unitActivity.NachatZapuskUnitaActivity)
	w.RegisterActivity(unitActivity.NachatOstanokuUnitaActivity)
	w.RegisterActivity(runner.RunnerHealthCheckActivity)

	// Start listening to the Task Queue
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("unable to start Worker", err)
	}

}
