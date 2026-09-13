// Package diagnostics records bounded, local operational data without request payloads.
package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/registration"
	"github.com/depthbomb/tomogo/rest"
	"modernc.org/sqlite"
)

type Options struct {
	Directory string
	MaxBytes  int64
	MaxFiles  int
}

type measurement struct {
	Category   string `json:"category"`
	Name       string `json:"name"`
	Outcome    string `json:"outcome"`
	Status     int    `json:"http_status,omitempty"`
	Code       int    `json:"discord_code,omitempty"`
	SQLiteCode int    `json:"sqlite_code,omitempty"`
}

type aggregate struct {
	measurement
	Count   uint64    `json:"count"`
	Items   uint64    `json:"items,omitempty"`
	TotalMS float64   `json:"total_ms"`
	MaxMS   float64   `json:"max_ms"`
	Buckets [8]uint64 `json:"duration_buckets"`
}

type record struct {
	Schema        int                `json:"schema"`
	Time          time.Time          `json:"time"`
	Run           string             `json:"run"`
	Event         string             `json:"event"`
	Operation     *measurement       `json:"operation,omitempty"`
	DurationMS    float64            `json:"duration_ms,omitempty"`
	Reference     string             `json:"reference,omitempty"`
	Summary       []aggregate        `json:"summary,omitempty"`
	Values        map[string]float64 `json:"values,omitempty"`
	Build         map[string]string  `json:"build,omitempty"`
	Dropped       uint64             `json:"dropped_total"`
	WriteErrors   uint64             `json:"write_errors_total"`
	WindowSeconds float64            `json:"window_seconds,omitempty"`
}

type Recorder struct {
	mu          sync.Mutex
	closed      bool
	series      map[measurement]aggregate
	queue       chan record
	stop        chan struct{}
	done        chan struct{}
	writer      io.WriteCloser
	run         string
	onError     func()
	windowStart time.Time
	dropped     atomic.Uint64
	writeErrors atomic.Uint64
}

var names = map[string][]string{
	"continuation": {"account.delete"},
	"command":      {"price", "about", "track", "track.add", "track.list", "track.remove", "freegames", "freegames.subscribe", "freegames.unsubscribe", "freegames.status", "account", "account.delete"},
	"job":          {"poll", "deliver", "catalog", "retention", "free-game-discovery", "free-game-delivery", "free-game-retention", "presence", "diagnostics"},
	"limit":        {"command", "price", "add", "account", "freegames"},
	"http":         {"steam", "discord", "freegames"},
	"cache":        {"price-hit", "price-miss", "artwork-hit", "artwork-miss", "steam-circuit-open"},
	"delivery":     {"sale-sent", "sale-retry", "sale-dead", "free-sent", "free-retry", "free-dead"},
	"source":       {"steam", "epic", "gog", "ubisoft"},
	"work":         {"poll", "catalog", "retention", "steam", "epic", "gog", "ubisoft"},
	"health":       {"process"},
	"rate_limit":   {"discord"},
	"gateway":      {"idle", "dialing", "awaiting_hello", "authenticating", "ready", "reconnecting", "closing", "stopped"},
	"framework":    {"gateway-raw-callback", "gateway-event-callback", "gateway-decode", "gateway-decode-callback", "gateway-state-callback", "gateway-state-hook", "interaction-http", "interactions-decode", "interactions", "events", "continuations", "schedules", "client-user"},
}

var gaugeNames = []string{
	"uptime_seconds", "heap_bytes", "heap_objects", "goroutines", "gc_cycles",
	"db_open", "db_in_use", "db_idle", "db_wait_count", "db_wait_seconds",
	"sale_pending", "sale_retry", "sale_sending", "sale_dead", "sale_oldest_seconds",
	"free_pending", "free_retry", "free_sending", "free_dead", "free_oldest_seconds",
	"runs", "failures", "skipped", "active", "offers", "problems", "items",
}

var referencePattern = regexp.MustCompile(`^[a-z][a-z0-9]{23}$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40,64}$`)
var versionPattern = regexp.MustCompile(`^(v[0-9][a-zA-Z0-9.+-]{0,100}|\(devel\))$`)

func classify(err error) (string, int, int) {
	if err == nil {
		return "ok", 0, 0
	}

	if _, ok := errors.AsType[*registration.PanicError](err); ok {
		return "panic", 0, 0
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", 0, 0
	}

	if network, ok := errors.AsType[net.Error](err); ok && network.Timeout() {
		return "timeout", 0, 0
	}

	if errors.Is(err, context.Canceled) {
		return "canceled", 0, 0
	}

	if remote, ok := errors.AsType[*rest.Error](err); ok {
		return "http_error", max(0, min(remote.StatusCode, 599)), max(0, min(remote.Code, 999999))
	}

	if errors.Is(err, preconditions.ErrDenied) {
		return "rejected", 0, 0
	}

	if _, ok := errors.AsType[*domain.PublicError](err); ok {
		return "invalid_request", 0, 0
	}

	return "error", 0, 0
}

func safeMeasurement(category, name string, err error) measurement {
	allowed, ok := names[category]
	if !ok {
		category = "other"
	}

	if !slices.Contains(allowed, name) {
		name = "other"
	}
	outcome, status, code := classify(err)

	op := measurement{
		Category: category,
		Name:     name,
		Outcome:  outcome,
		Status:   status,
		Code:     code,
	}
	if database, ok := errors.AsType[*sqlite.Error](err); ok {
		op.SQLiteCode = database.Code()
	}

	return op
}

func buildMetadata() map[string]string {
	build := map[string]string{
		"go":   runtime.Version(),
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return build
	}

	if versionPattern.MatchString(info.Main.Version) {
		build["version"] = info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && revisionPattern.MatchString(setting.Value) {
			build["revision"] = setting.Value
		}

		if setting.Key == "vcs.modified" && (setting.Value == "true" || setting.Value == "false") {
			build["modified"] = setting.Value
		}
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/depthbomb/tomogo" && versionPattern.MatchString(dep.Version) {
			build["tomogo"] = dep.Version
			if dep.Replace != nil {
				build["tomogo_replaced"] = "true"
			}
		}
	}

	return build
}

func newRecorder(writer io.WriteCloser, onError func()) *Recorder {
	r := &Recorder{
		series:      make(map[measurement]aggregate),
		queue:       make(chan record, 256),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		writer:      writer,
		run:         cuid.Generate(),
		onError:     onError,
		windowStart: time.Now(),
	}
	go r.loop()

	return r
}

func (r *Recorder) enqueueLocked(event record) {
	event.Time = time.Now().UTC()
	select {
	case r.queue <- event:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) write(event record) {
	event.Schema = 1
	event.Run = r.run
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	event.Dropped = r.dropped.Load()
	event.WriteErrors = r.writeErrors.Load()
	data, err := json.Marshal(event)
	if err == nil {
		data = append(data, '\n')
		var n int
		n, err = r.writer.Write(data)
		if n != len(data) && err == nil {
			err = io.ErrShortWrite
		}
	}

	if err != nil {
		r.dropped.Add(1)
		if r.writeErrors.Add(1) == 1 && r.onError != nil {
			r.onError()
		}
	}
}

func (r *Recorder) flush() {
	r.mu.Lock()
	series := r.series
	r.series = make(map[measurement]aggregate)
	r.mu.Unlock()
	items := make([]aggregate, 0, len(series))
	for _, item := range series {
		items = append(items, item)
	}
	window := time.Since(r.windowStart).Seconds()
	r.windowStart = time.Now()
	r.write(record{
		Event:         "summary",
		Summary:       items,
		WindowSeconds: window,
		Build:         buildMetadata(),
	})
}

func (r *Recorder) loop() {
	defer close(r.done)
	defer func() {
		if err := r.writer.Close(); err != nil {
			r.writeErrors.Add(1)
		}
	}()
	r.write(record{
		Event: "startup",
		Build: buildMetadata(),
	})
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case event := <-r.queue:
			r.write(event)
		case <-ticker.C:
			r.flush()
		case <-r.stop:
			for {
				select {
				case event := <-r.queue:
					r.write(event)
				default:
					r.flush()
					r.write(record{
						Event: "shutdown",
					})

					return
				}
			}
		}
	}
}

// Observe summarizes activity and records individual unexpected failures.
// Names are allowlisted; errors are classified without formatting their text.
func (r *Recorder) Observe(category, name string, elapsed time.Duration, err error) {
	if r == nil {
		return
	}
	op := safeMeasurement(category, name, err)
	r.observe(op, elapsed, err)
}

func (r *Recorder) observe(op measurement, elapsed time.Duration, err error) {
	ms := max(0, float64(elapsed)/float64(time.Millisecond))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	item, exists := r.series[op]
	if !exists && len(r.series) >= 256 {
		r.dropped.Add(1)

		return
	}
	item.measurement = op
	item.Count++
	item.TotalMS += ms
	item.MaxMS = max(item.MaxMS, ms)
	bucket := 0
	for _, bound := range []float64{10, 50, 100, 500, 1000, 5000, 30000} {
		if ms <= bound {
			break
		}
		bucket++
	}
	item.Buckets[bucket]++
	r.series[op] = item
	if err != nil && op.Outcome != "canceled" && op.Outcome != "rejected" && op.Outcome != "invalid_request" {
		r.enqueueLocked(record{
			Event:      "failure",
			Operation:  &op,
			DurationMS: ms,
		})
	}
}

// Add summarizes processed item counts without producing an event per batch.
func (r *Recorder) Add(name string, count int) {
	if r == nil || count <= 0 {
		return
	}
	op := safeMeasurement("work", name, nil)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	item, exists := r.series[op]
	if !exists && len(r.series) >= 256 {
		r.dropped.Add(1)

		return
	}
	item.measurement = op
	item.Count++
	item.Items += uint64(count)
	r.series[op] = item
}

// Reference connects an existing generated support reference to a sanitized failure.
func (r *Recorder) Reference(name, reference string, err error) {
	if r == nil || !referencePattern.MatchString(reference) {
		return
	}
	op := safeMeasurement("command", name, err)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.enqueueLocked(record{
			Event:     "reference",
			Operation: &op,
			Reference: reference,
		})
	}
}

// Sample copies only known numeric gauges. Caller-owned maps are never retained.
func (r *Recorder) Sample(category, name string, values map[string]float64) {
	if r == nil {
		return
	}
	safe := make(map[string]float64)
	for key, value := range values {
		if slices.Contains(gaugeNames, key) && !math.IsNaN(value) && !math.IsInf(value, 0) {
			safe[key] = value
		}
	}
	op := safeMeasurement(category, name, nil)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.enqueueLocked(record{
			Event:     "sample",
			Operation: &op,
			Values:    safe,
		})
	}
}

// Close stops admission and drains accepted records within the caller's deadline.
func (r *Recorder) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.stop)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		if r.writeErrors.Load() != 0 {
			return errors.New("diagnostics records could not all be written")
		}

		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func Open(options Options, onError func()) (*Recorder, error) {
	writer, err := openFiles(options)
	if err != nil {
		return nil, err
	}

	return newRecorder(writer, onError), nil
}
