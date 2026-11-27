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
	workspaceActivity "runner/temporal/activity/workspace"
	"runner/temporal/utils"
	"syscall"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
)

func Init(config config.Configuration) {
	// Create the client object just once per process
	c, err := utils.MakeTemporalClient(config)

	if err != nil {
		log.Fatalln("unable to connect to temporal", err)
		return
	}

	defer c.Close()

	// Очередь Temporal всегда равна runnerId из config.json. Отдельная
	// taskQueue не хранится, legacy-константы не используются.
	taskQueue := config.RunnerID

	// This worker hosts both Workflow and Activity functions
	w := worker.New(c, taskQueue, worker.Options{})

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
		// Идентичность и per-runner секрет берутся из config.json. Секрет
		// подписывает saveSteps (responseId+stepsContent) и другие callback-и.
		RunnerID: config.RunnerID,
		Secret:   config.RunnerSecret,
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

	w.RegisterActivity(projectActivity.RemoveProjectActivity)
	w.RegisterActivity(unitActivity.NachatSborkuUnitaActivity)
	w.RegisterActivity(unitActivity.NachatUdalenieUnitaActivity)
	w.RegisterActivity(unitActivity.NachatObnovlenieUnitaActivity)
	w.RegisterActivity(unitActivity.NachatPodgotovkuUnitaActivity)
	w.RegisterActivity(unitActivity.NachatSbrosPodgotovkiUnitaActivity)

	// Редакторские сессии юнита (plan §8). Имена активностей фиксированы:
	// PHP-workflow вызывает их по литеральному имени.
	w.RegisterActivityWithOptions(
		unitActivity.IzmenitRezhimRedaktoraUnitaActivity,
		activity.RegisterOptions{Name: "IzmenitRezhimRedaktoraUnitaActivity"},
	)
	w.RegisterActivityWithOptions(
		unitActivity.PodgotovitSessiyuRedaktoraActivity,
		activity.RegisterOptions{Name: "PodgotovitSessiyuRedaktoraActivity"},
	)
	w.RegisterActivity(unitActivity.NachatZapuskUnitaActivity)
	w.RegisterActivity(unitActivity.NachatOstanokuUnitaActivity)
	w.RegisterActivity(unitActivity.NachatDeistvieUnitaActivity)
	w.RegisterActivity(unitActivity.ProveritKonteinerUnitaActivity)

	// Workspace-стенды: отдельные контейнеры и тома, та же очередь.
	w.RegisterActivity(workspaceActivity.ZapustitWorkspaceActivity)
	w.RegisterActivity(workspaceActivity.ObnovitWorkspaceActivity)
	w.RegisterActivity(workspaceActivity.OstanovitWorkspaceActivity)
	w.RegisterActivity(workspaceActivity.UdalitWorkspaceActivity)
	w.RegisterActivity(workspaceActivity.ProveritIzmeneniyaWorkspaceActivity)

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

	// Идентичность и HMAC-секрет callback-ов берутся только из config.json
	// (RunnerID и RunnerSecret обязательны), без env-fallback.
	monitorClient := monitor.NewClient(config, backendURL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go monitor.RunHealth(ctx, store, monitorClient, workdir)
	go monitor.RunResources(ctx, workdir)
	go monitor.RunReconciler(ctx, store, monitorClient, workdir)
	go monitor.RunCleanup(ctx, workdir)

	// Start listening to the Task Queue
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("unable to start Worker", err)
	}

}
