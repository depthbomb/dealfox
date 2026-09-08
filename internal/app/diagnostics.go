package app

import (
	"context"
	"runtime"
	"time"

	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/tomogo/registration"
	"github.com/depthbomb/tomogo/schedule"
)

func observeJob(recorder *diagnostics.Recorder, job schedule.Job) schedule.Job {
	if recorder == nil {
		return job
	}
	handler := job.Handler
	job.Handler = func(ctx context.Context, run schedule.Run) (err error) {
		start := time.Now()
		returned := false
		defer func() {
			failure := err
			if !returned {
				failure = &registration.PanicError{}
			} else if failure == nil {
				failure = ctx.Err()
			}
			recorder.Observe("job", run.Name, time.Since(start), failure)
		}()
		err = handler(ctx, run)
		returned = true

		return err
	}

	return job
}

func diagnosticJob(recorder *diagnostics.Recorder, db *store.Store, manager *schedule.Manager) schedule.Job {
	started := time.Now()

	return periodicJob("diagnostics", time.Minute, 10*time.Second, func(ctx context.Context) error {
		queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		values, err := db.Diagnostics(queryCtx)
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		values["uptime_seconds"] = time.Since(started).Seconds()
		values["heap_bytes"] = float64(memory.HeapAlloc)
		values["heap_objects"] = float64(memory.HeapObjects)
		values["goroutines"] = float64(runtime.NumGoroutine())
		values["gc_cycles"] = float64(memory.NumGC)
		recorder.Sample("health", "process", values)
		for _, snapshot := range manager.List() {
			recorder.Sample("job", snapshot.Name, map[string]float64{
				"runs":     float64(snapshot.Runs),
				"failures": float64(snapshot.Failures),
				"skipped":  float64(snapshot.Skipped),
				"active":   float64(snapshot.Active),
			})
		}

		return err
	})
}
