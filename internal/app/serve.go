package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/discord"
	"github.com/depthbomb/dealfox/internal/discord/commands"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/steam"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/dealfox/internal/worker"
	"github.com/depthbomb/tomogo"
	"github.com/depthbomb/tomogo/gateway"
	"github.com/depthbomb/tomogo/rest"
	"github.com/depthbomb/tomogo/schedule"
)

type applicationLifecycle interface {
	Start(context.Context) error
	Wait(context.Context) error
}

func runApplication(ctx context.Context, app applicationLifecycle, tasks ...*schedule.Task) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, task := range tasks {
		defer task.Cancel()
	}

	if err := app.Start(runCtx); err != nil {
		return err
	}

	return app.Wait(context.WithoutCancel(ctx))
}

func serve(ctx context.Context, cfg *config.Config, db *store.Store, logger *slog.Logger) (err error) {
	if cfg.BotToken == nil || cfg.SteamAPIKey == nil {
		return errors.New("BOT_TOKEN and STEAM_WEB_API_KEY are required for serve")
	}

	unlock, err := db.LockProcess(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var recorder *diagnostics.Recorder
	if cfg.DiagnosticsEnabled {
		recorder, err = diagnostics.Open(diagnostics.Options{
			Directory: cfg.DiagnosticsDirectory,
			MaxBytes:  10 * 1024 * 1024,
			MaxFiles:  10,
		}, func() {
			logger.Warn("diagnostics writer failed; some records may be lost")
		})
		if err != nil {
			logger.Warn("diagnostics unavailable; continuing without file recording")
		}
	}
	defer func() {
		recorder.Observe("health", "process", 0, err)
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := recorder.Close(closeCtx); closeErr != nil {
			logger.Warn("diagnostics shutdown incomplete")
		}
	}()
	if err := db.Recover(ctx, true); err != nil {
		return err
	}

	if err := db.InitializeFreeMonitors(ctx); err != nil {
		return err
	}

	if err := db.RecoverFree(ctx, true); err != nil {
		return err
	}

	steamClient := steam.New(cfg)
	steamClient.Diagnostics = recorder
	if recorder != nil {
		steamClient.HTTP.Transport = diagnostics.Transport{
			Recorder: recorder,
			Service:  "steam",
			Base:     steamClient.HTTP.Transport,
		}
	}
	service := &tracker.Service{
		Store:  db,
		Steam:  steamClient,
		Config: cfg,
	}
	commandHandler := &commands.Handler{
		Tracker:     service,
		Logger:      logger,
		Diagnostics: recorder,
	}
	app, err := tomogo.New(tomogo.Config{
		Delivery:                tomogo.DeliveryGateway,
		Token:                   cfg.BotToken.Release(),
		Intents:                 gateway.IntentGuilds | gateway.IntentDirectMessages,
		InteractionErrorHandler: commandHandler.HandleInteractionError,
		ScheduleShutdownTimeout: scheduleShutdownTimeout,
		REST: rest.Config{
			Observer: diagnostics.RESTObserver{
				Recorder: recorder,
			},
		},
		Hooks: tomogo.Hooks{
			Error: func(_ context.Context, e tomogo.ErrorEvent) {
				recorder.Observe("framework", e.Subsystem, 0, e.Err)
				logger.Error("application error", "subsystem", e.Subsystem, "name", e.Name, "error", e.Err)
			},
			GatewayState: func(change gateway.StateChange) {
				recorder.Observe("gateway", string(change.Current), 0, nil)
				logger.Info("Discord Gateway state changed", "shard", change.ShardID, "previous", change.Previous, "current", change.Current)
			},
		},
	})
	if err != nil {
		return err
	}

	commandHandler.REST = app.REST()
	if err := commandHandler.Register(app); err != nil {
		return err
	}

	w := &worker.Worker{
		Tracker: service,
		Sender: discord.Sender{
			REST:  app.REST(),
			Steam: steamClient,
		},
		Logger:      logger,
		Diagnostics: recorder,
	}
	freeWorker := &worker.FreeGames{
		Store:   db,
		Scanner: freegames.NewClient(recorder),
		Sender: discord.Sender{
			REST: app.REST(),
		},
		Config:      cfg,
		Logger:      logger,
		Diagnostics: recorder,
	}
	jobs := workerJobs(cfg, w, freeWorker)
	if recorder != nil {
		jobs = append(jobs, diagnosticJob(recorder, db, app.Schedules()))
	}
	for i, job := range jobs {
		jobs[i] = observeJob(recorder, job)
	}
	tasks, err := registerJobs(app.Schedules(), jobs)
	if err != nil {
		return err
	}
	logger.Info("starting DealFox")

	return runApplication(ctx, app, tasks...)
}
