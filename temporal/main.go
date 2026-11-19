package temporal

import (
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"log"
	"net/http"
	"os"
	"runner/config"
	opencodeActivity "runner/temporal/activity/opencode"
	projectActivity "runner/temporal/activity/project"
	"runner/temporal/activity/runner"
	unitActivity "runner/temporal/activity/unit"
	"runner/temporal/utils"
	"time"
)

func Init(config config.Configuration) {
	// Create the client object just once per process
	c, err := utils.MakeTemporalClient(config)

	if err != nil {
		log.Fatalln("unable to connect to temporal", err)
		return
	}

	defer c.Close()

	// This worker hosts both Workflow and Activity functions
	w := worker.New(c, "unitman-runner-queue", worker.Options{})

	backendURL := os.Getenv("RUNNER_BACKEND_URL")
	if backendURL == "" {
		backendURL = config.ApiHost
	}
	opencodeActs := &opencodeActivity.Activities{
		BackendURL: backendURL,
		Secret:     os.Getenv("RUNNER_CALLBACK_SECRET"),
		// Прогресс — короткие запросы; у OpenCode свой дедлайн хода, поэтому
		// общий Timeout клиента ему не подходит.
		Client:   &http.Client{Timeout: 10 * time.Second},
		OpenCode: opencodeActivity.NewCLIExecutor(),
		// Корень раскладки юнитов (<root>/projects/<ProjectId>/units/<UnitId>).
		// По умолчанию совпадает с рабочим каталогом раннера.
		WorkDir: os.Getenv("RUNNER_WORK_DIR"),
	}
	w.RegisterActivityWithOptions(
		opencodeActs.VipolnitHodOpenCodeActivity,
		activity.RegisterOptions{Name: opencodeActivity.ActivityName},
	)

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
	w.RegisterActivity(runner.OchistkaDockeraActivity)
	w.RegisterActivity(unitActivity.NachatDeistvieUnitaActivity)
	w.RegisterActivity(unitActivity.ProveritKonteinerUnitaActivity)

	// Start listening to the Task Queue
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("unable to start Worker", err)
	}

}
