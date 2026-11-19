package temporal

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runner/config"
	"runner/monitor"
	"runner/state"
	opencodeActivity "runner/temporal/activity/opencode"
	projectActivity "runner/temporal/activity/project"
	unitActivity "runner/temporal/activity/unit"
	"runner/temporal/utils"
	"syscall"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
)

// TaskQueue — очередь Temporal, которую обслуживает воркер раннера.
// Используется и в воркере, и в health-push (payload TaskQueue).
const TaskQueue = "unitman-runner-queue"

func Init(config config.Configuration) {
	// Create the client object just once per process
	c, err := utils.MakeTemporalClient(config)

	if err != nil {
		log.Fatalln("unable to connect to temporal", err)
		return
	}

	defer c.Close()

	// This worker hosts both Workflow and Activity functions
	w := worker.New(c, TaskQueue, worker.Options{})

	backendURL := os.Getenv("RUNNER_BACKEND_URL")
	if backendURL == "" {
		backendURL = config.ApiHost
	}

	workdir := os.Getenv("RUNNER_WORK_DIR")
	if workdir == "" {
		workdir, err = os.Getwd()
		if err != nil {
			log.Printf("unable to resolve working directory: %v", err)
			workdir = "."
		}
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
		WorkDir: workdir,
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
	w.RegisterActivity(unitActivity.NachatDeistvieUnitaActivity)
	w.RegisterActivity(unitActivity.ProveritKonteinerUnitaActivity)

	// Состояние контейнеров юнитов. При ошибке открытия БД раннер не падает:
	// state.Default() вернёт no-op store, мониторинг продолжит работать без
	// сохранения состояния.
	dbPath := os.Getenv("RUNNER_STATE_DB")
	if dbPath == "" {
		dbPath = "runner-state.db"
	}
	if err := state.Init(dbPath); err != nil {
		log.Printf("state: unable to init %q, continuing with no-op store: %v", dbPath, err)
	}
	store := state.Default()

	runnerID := os.Getenv("RUNNER_ID")
	if runnerID == "" {
		runnerID, err = os.Hostname()
		if err != nil {
			log.Printf("unable to resolve hostname for RUNNER_ID: %v", err)
			runnerID = "unknown-runner"
		}
	}

	monitorClient := &monitor.Client{
		BaseURL:    backendURL,
		Secret:     os.Getenv("RUNNER_CALLBACK_SECRET"),
		RunnerID:   runnerID,
		TaskQueue:  TaskQueue,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go monitor.RunHealth(ctx, store, monitorClient, workdir)
	go monitor.RunReconciler(ctx, store, monitorClient, workdir)
	go monitor.RunCleanup(ctx, workdir)

	// Start listening to the Task Queue
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("unable to start Worker", err)
	}

}
