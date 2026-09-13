package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depthbomb/nook"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/registration"
	"github.com/depthbomb/tomogo/rest"
)

type memorySink struct {
	bytes.Buffer
	closeErr error
}

type blockedSink struct {
	memorySink
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type failedSink struct{}

type unprintableError struct{}

func (w *memorySink) Close() error {
	return w.closeErr
}

func (w *blockedSink) Write(data []byte) (int, error) {
	w.once.Do(func() {
		close(w.started)
	})
	<-w.release

	return w.Buffer.Write(data)
}

func (failedSink) Write([]byte) (int, error) {
	return 0, errors.New("disk unavailable")
}

func (failedSink) Close() error {
	return nil
}

func (unprintableError) Error() string {
	panic("diagnostics must never format arbitrary errors")
}

func readRecords(t *testing.T, data []byte) []record {
	t.Helper()
	var records []record
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var event record
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatal(err)
		}
		records = append(records, event)
	}

	return records
}

func TestRecorderSummarizesAndRedacts(t *testing.T) {
	sink := &memorySink{}
	r := newRecorder(sink, nil)
	for range 10 {
		r.Observe("command", "track.add", 25*time.Millisecond, nil)
	}
	r.Add("catalog", 50000)
	r.Observe("private-category", "private-name", 0, unprintableError{})
	r.Observe("command", "price", 0, &preconditions.Failure{
		Reason: "private-reason",
	})
	r.Observe("job", "poll", 0, &registration.PanicError{
		Value: "private-panic",
		Stack: []byte("private-stack"),
	})
	db, err := nook.Create(t.Context(), filepath.Join(t.TempDir(), "diagnostics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, databaseErr := db.ExecContext(t.Context(), "SELECT private_database_row FROM missing_table")
	if databaseErr == nil {
		t.Fatal("expected database failure")
	}
	r.Observe("job", "retention", 0, databaseErr)
	reference := "abcdefghijklmnopqrstuvwx"
	r.Reference("price", reference, errors.New("private-error"))
	r.Reference("price", "private-invalid-reference", errors.New("private-error"))
	gauges := map[string]float64{
		"heap_bytes":      1234,
		"private-user-id": 999999,
	}
	r.Sample("health", "process", gauges)
	gauges["heap_bytes"] = 0
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	data := sink.Bytes()
	if bytes.Contains(data, []byte("private-")) {
		t.Fatal("diagnostics exposed private input")
	}
	records := readRecords(t, data)
	var command aggregate
	var failures, references, samples int
	for _, event := range records {
		if event.Schema != 1 || event.Run == "" || event.Time.IsZero() {
			t.Fatal("missing record identity")
		}

		if event.Event == "failure" {
			failures++
		}

		if event.Event == "reference" && event.Reference == reference {
			references++
		}

		if event.Event == "sample" && event.Values["heap_bytes"] == 1234 {
			samples++
		}
		for _, item := range event.Summary {
			if item.Name == "track.add" {
				command = item
			}
		}
	}
	if command.Count != 10 || command.TotalMS != 250 || command.MaxMS != 25 || command.Buckets[1] != 10 || failures != 3 || references != 1 || samples != 1 {
		t.Fatalf("incorrect summary: %+v; failures=%d references=%d samples=%d", command, failures, references, samples)
	}
}

func TestRecorderFlushesEveryMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &memorySink{}
		r := newRecorder(sink, nil)
		r.Observe("cache", "price-hit", 0, nil)
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		records := readRecords(t, sink.Bytes())
		if len(records) != 2 || records[1].Event != "summary" || records[1].WindowSeconds != 60 || len(records[1].Summary) != 1 {
			t.Fatalf("minute summary = %+v", records)
		}

		if err := r.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSlowWriterDoesNotBlockProducersOrUnboundShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &blockedSink{
			started: make(chan struct{}),
			release: make(chan struct{}),
		}
		r := newRecorder(sink, nil)
		<-sink.started
		var producers sync.WaitGroup
		for range 10 {
			producers.Go(func() {
				for range 100 {
					r.Observe("job", "poll", time.Second, errors.New("failed"))
				}
			})
		}
		producers.Wait()
		if r.dropped.Load() == 0 || len(r.queue) != cap(r.queue) {
			t.Fatal("slow writer did not exercise bounded admission")
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := r.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked shutdown = %v", err)
		}
		close(sink.release)
		if err := r.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		var count uint64
		for _, event := range readRecords(t, sink.Bytes()) {
			for _, item := range event.Summary {
				count += item.Count
			}
		}
		if count != 1000 {
			t.Fatalf("slow writer lost aggregate observations: %d", count)
		}
	})
}

func TestRecorderBoundsCardinalityAndReportsWriterFailure(t *testing.T) {
	var warnings atomic.Int64
	r := newRecorder(failedSink{}, func() {
		warnings.Add(1)
	})
	for code := range 1000 {
		r.Observe("http", "discord", 0, &rest.Error{
			StatusCode: 400,
			Code:       code,
		})
	}
	r.mu.Lock()
	count := len(r.series)
	r.mu.Unlock()
	if count > 256 || r.dropped.Load() == 0 {
		t.Fatal("series were not bounded")
	}

	if err := r.Close(t.Context()); err == nil || warnings.Load() != 1 {
		t.Fatalf("writer failure = %v, warnings = %d", err, warnings.Load())
	}
	r = newRecorder(&memorySink{
		closeErr: errors.New("sync failed"),
	}, nil)
	if err := r.Close(t.Context()); err == nil {
		t.Fatal("final sync failure was ignored")
	}
}

func TestFilesRotateAcrossRestartsAndRepairPartialRecords(t *testing.T) {
	dir := t.TempDir()
	options := Options{
		Directory: dir,
		MaxBytes:  65536,
		MaxFiles:  3,
	}
	line := []byte(`{"value":"` + strings.Repeat("x", 20000) + "\"}\n")
	for range 2 {
		writer, err := openFiles(options)
		if err != nil {
			t.Fatal(err)
		}
		for range 10 {
			if _, err := writer.Write(line); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("rotated files = %d, error = %v", len(entries), err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || len(data) > int(options.MaxBytes) {
			t.Fatalf("file exceeded budget: %s, %v", entry.Name(), err)
		}
		for _, row := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			if !json.Valid(row) {
				t.Fatal("rotation split a JSON record")
			}
		}
	}
	path := filepath.Join(dir, "beta.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"partial":`)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	writer, err := openFiles(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("{\"complete\":true}\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte("partial")) {
		t.Fatal("incomplete record survived restart")
	}
}
