package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/worker"
	"github.com/depthbomb/tomogo"
	"github.com/depthbomb/tomogo/schedule"
)

type lifecycleStub struct {
	start func(context.Context) error
	wait  func(context.Context) error
}

func (a lifecycleStub) Start(ctx context.Context) error {
	return a.start(ctx)
}

func (a lifecycleStub) Wait(ctx context.Context) error {
	return a.wait(ctx)
}

func TestRunApplicationPreservesLifecycleErrors(t *testing.T) {
	for _, startup := range []bool{true, false} {
		ctx, cancel := context.WithCancel(t.Context())
		failure := errors.New("application failed")
		app := lifecycleStub{
			start: func(context.Context) error {
				if startup {
					return failure
				}
				cancel()

				return nil
			},
			wait: func(ctx context.Context) error {
				if startup || ctx.Err() != nil {
					t.Error("wait called after rejected startup or with canceled context")
				}

				return failure
			},
		}
		err := runApplication(ctx, app)
		cancel()
		if !errors.Is(err, failure) {
			t.Fatalf("lifecycle error = %v", err)
		}
	}
}

func TestWorkerSchedulesStartImmediatelyAndRepeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testutil.Config(t)
		cfg.DeliveryPollInterval = 3 * time.Second
		cfg.CatalogSyncInterval = 8 * time.Hour
		cfg.RetentionInterval = 12 * time.Hour
		jobs := workerJobs(cfg, &worker.Worker{}, &worker.FreeGames{})
		intervals := map[string]time.Duration{
			"poll":                time.Second,
			"deliver":             cfg.DeliveryPollInterval,
			"catalog":             cfg.CatalogSyncInterval,
			"retention":           cfg.RetentionInterval,
			"free-game-discovery": 30 * time.Minute,
			"free-game-delivery":  cfg.DeliveryPollInterval,
			"free-game-retention": cfg.RetentionInterval,
		}
		if len(jobs) != len(intervals) {
			t.Fatalf("got %d jobs", len(jobs))
		}
		calls := make([]atomic.Int64, len(jobs))
		for i := range jobs {
			job := &jobs[i]
			next, ok := job.Schedule.Next(time.Now())
			if !ok || next.Sub(time.Now()) != intervals[job.Name] {
				t.Fatalf("unexpected interval for %s: %v", job.Name, next)
			}

			if job.Timeout <= time.Minute {
				t.Fatalf("job %s needs a longer timeout", job.Name)
			}
			job.Handler = func(ctx context.Context, run schedule.Run) error {
				deadline, ok := ctx.Deadline()
				if !ok || deadline.Sub(time.Now()) != job.Timeout || run.Name != job.Name {
					t.Errorf("incorrect execution context for %s", job.Name)
				}
				count := calls[i].Add(1)
				if run.Manual || run.Startup != (count == 1) {
					t.Errorf("incorrect run origin for %s: manual=%t startup=%t count=%d", job.Name, run.Manual, run.Startup, count)
				}

				return nil
			}
		}
		app, err := tomogo.New(tomogo.Config{
			Delivery:                tomogo.DeliveryHTTP,
			ScheduleShutdownTimeout: scheduleShutdownTimeout,
			Schedules: schedule.Config{
				WaitReady: func(context.Context) error {
					t.Error("background work waited for Gateway readiness")

					return errors.New("not ready")
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := registerJobs(app.Schedules(), jobs)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- runApplication(ctx, app, tasks...)
		}()
		synctest.Wait()
		for i, job := range jobs {
			if calls[i].Load() != 1 {
				t.Fatalf("%s startup calls = %d", job.Name, calls[i].Load())
			}
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		for i, job := range jobs {
			want := int64(1 + 3*time.Second/intervals[job.Name])
			if calls[i].Load() != want {
				t.Errorf("%s recurring calls = %d, want %d", job.Name, calls[i].Load(), want)
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestScheduledJobTimeoutSkipsOverlapAndReportsFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan tomogo.ErrorEvent, 2)
		app, err := tomogo.New(tomogo.Config{
			Delivery:                tomogo.DeliveryHTTP,
			ScheduleShutdownTimeout: scheduleShutdownTimeout,
			Hooks: tomogo.Hooks{
				Error: func(_ context.Context, event tomogo.ErrorEvent) {
					events <- event
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := registerJobs(app.Schedules(), []schedule.Job{
			periodicJob("deliver", time.Second, 2*time.Second, func(ctx context.Context) error {
				<-ctx.Done()
				// Simulate saving the delivery result after request cancellation.
				time.Sleep(10 * time.Second)

				return ctx.Err()
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- runApplication(ctx, app, tasks...)
		}()
		synctest.Wait()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		task := tasks[0]
		if execution, err := task.TriggerRun(); execution != nil || !errors.Is(err, schedule.ErrBusy) {
			t.Fatalf("trigger during finalization = %v", err)
		}
		snapshot := task.Snapshot()
		if snapshot.Runs != 1 || snapshot.Active != 1 || snapshot.Skipped == 0 {
			t.Fatalf("job overlapped during timeout cleanup: %+v", snapshot)
		}
		event := <-events
		if event.Subsystem != tomogo.ErrorSubsystemSchedules || event.Name != "deliver" || !errors.Is(event.Err, context.DeadlineExceeded) {
			t.Fatalf("timeout event = %+v", event)
		}
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if task.Snapshot().Runs != 2 {
			t.Fatal("job did not resume after timeout cleanup")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		if task.Snapshot().Failures != 1 || len(events) != 0 {
			t.Fatal("normal shutdown was reported as a job failure")
		}
	})
}

func TestRunApplicationDrainsJobsAndReportsShutdownTimeout(t *testing.T) {
	for _, cleanup := range []time.Duration{10 * time.Second, scheduleShutdownTimeout + time.Second} {
		t.Run(cleanup.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app, err := tomogo.New(tomogo.Config{
					Delivery:                tomogo.DeliveryHTTP,
					ScheduleShutdownTimeout: scheduleShutdownTimeout,
				})
				if err != nil {
					t.Fatal(err)
				}
				var drained atomic.Bool
				tasks, err := registerJobs(app.Schedules(), []schedule.Job{
					periodicJob("deliver", time.Hour, time.Minute, func(ctx context.Context) error {
						<-ctx.Done()
						time.Sleep(cleanup)
						drained.Store(true)

						return ctx.Err()
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					done <- runApplication(ctx, app, tasks...)
				}()
				synctest.Wait()
				cancel()
				err = <-done
				if cleanup < scheduleShutdownTimeout {
					if err != nil || !drained.Load() {
						t.Fatalf("shutdown error = %v, drained = %t", err, drained.Load())
					}
				} else if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("shutdown deadline error = %v", err)
				}
				<-tasks[0].Done()
			})
		})
	}
}

func TestRegisterJobsRollsBackPartialRegistration(t *testing.T) {
	manager, err := schedule.New(schedule.Config{})
	if err != nil {
		t.Fatal(err)
	}
	job := periodicJob("poll", time.Second, time.Minute, func(context.Context) error {
		return nil
	})
	if _, err := registerJobs(manager, []schedule.Job{job, job}); err == nil {
		t.Fatal("duplicate registration succeeded")
	}

	if len(manager.List()) != 0 {
		t.Fatal("failed registration left jobs installed")
	}
}

func TestRunApplicationCleansUpRejectedStartup(t *testing.T) {
	app, err := tomogo.New(tomogo.Config{
		Delivery: tomogo.DeliveryHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := registerJobs(app.Schedules(), []schedule.Job{
		periodicJob("poll", time.Hour, time.Minute, func(context.Context) error {
			t.Error("job ran after startup was rejected")

			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = runApplication(ctx, app, tasks...)
	if !errors.Is(err, context.Canceled) || len(app.Schedules().List()) != 0 {
		t.Fatalf("startup error = %v, remaining jobs = %v", err, app.Schedules().List())
	}
	<-tasks[0].Done()
}

func TestStartupCoalescesAnAlreadyDueRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, err := tomogo.New(tomogo.Config{
			Delivery: tomogo.DeliveryHTTP,
		})
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int64
		job := periodicJob("poll", time.Second, time.Minute, func(context.Context) error {
			calls.Add(1)

			return nil
		})
		tasks, err := registerJobs(app.Schedules(), []schedule.Job{job})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- runApplication(ctx, app, tasks...)
		}()
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("startup duplicated an already due run: %d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("startup changed the recurring cadence")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
