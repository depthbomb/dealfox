package diagnostics

import (
	"net/http"
	"time"

	"github.com/depthbomb/tomogo/rest"
)

type Transport struct {
	Recorder *Recorder
	Service  string
	Base     http.RoundTripper
}

type RESTObserver struct {
	Recorder *Recorder
}

// HTTP records an attempt without retaining its URL, headers, or response body.
func (r *Recorder) HTTP(service string, elapsed time.Duration, status int, err error) {
	if r == nil {
		return
	}
	op := safeMeasurement("http", service, err)
	if status >= 100 && status <= 599 {
		op.Status = status
	}

	if status >= 400 && err == nil {
		err = &rest.Error{
			StatusCode: status,
		}
		op.Outcome = "http_error"
	}
	r.observe(op, elapsed, err)
}

// RoundTrip measures transport latency through receipt of response headers.
func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	start := time.Now()
	response, err := base.RoundTrip(req)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	t.Recorder.HTTP(t.Service, time.Since(start), status, err)

	return response, err
}

func (o RESTObserver) RequestStarted(rest.Route, int) {}

func (o RESTObserver) RequestFinished(_ rest.Route, response *rest.Response, err error, elapsed time.Duration) {
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	o.Recorder.HTTP("discord", elapsed, status, err)
}

func (o RESTObserver) RateLimited(_ rest.Route, limit rest.RateLimit) {
	o.Recorder.Observe("rate_limit", "discord", max(limit.RetryAfter, limit.ResetAfter), nil)
}
