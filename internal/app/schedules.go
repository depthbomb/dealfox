package app

import (
	"context"
	"fmt"
	"time"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/worker"
	"github.com/depthbomb/tomogo/schedule"
)

// Delivery workers need up to ten seconds after cancellation to save results.
const scheduleShutdownTimeout = 30 * time.Second

func periodicJob(name string, interval, timeout time.Duration, run func(context.Context) error) schedule.Job {
	return schedule.Job{
		Name:           name,
		Schedule:       schedule.Every(interval),
		Timeout:        timeout,
		Overlap:        schedule.SkipIfRunning,
		WaitUntilReady: false,
		RunOnStart:     true,
		Handler: func(ctx context.Context, _ schedule.Run) error {
			return run(ctx)
		},
	}
}

func workerJobs(cfg *config.Config, sales *worker.Worker, free *worker.FreeGames) []schedule.Job {
	return []schedule.Job{
		// Large price batches can split into many sequential Steam requests.
		periodicJob("poll", time.Second, time.Hour, sales.Poll),
		periodicJob("deliver", cfg.DeliveryPollInterval, 2*time.Minute, sales.Dispatch),
		periodicJob("catalog", cfg.CatalogSyncInterval, time.Hour, sales.SyncCatalog),
		periodicJob("retention", cfg.RetentionInterval, 10*time.Minute, sales.Purge),
		// Each of the four sources already has its own five-minute timeout.
		periodicJob("free-game-discovery", 30*time.Minute, 30*time.Minute, free.Poll),
		periodicJob("free-game-delivery", cfg.DeliveryPollInterval, 2*time.Minute, free.Dispatch),
		periodicJob("free-game-retention", cfg.RetentionInterval, 10*time.Minute, free.Purge),
	}
}

func registerJobs(manager *schedule.Manager, jobs []schedule.Job) ([]*schedule.Task, error) {
	tasks := make([]*schedule.Task, 0, len(jobs))
	for _, job := range jobs {
		task, err := manager.Register(job)
		if err != nil {
			for _, registered := range tasks {
				registered.Cancel()
			}

			return nil, fmt.Errorf("register job %s: %w", job.Name, err)
		}
		tasks = append(tasks, task)
	}

	return tasks, nil
}
