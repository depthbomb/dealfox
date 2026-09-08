package diagnostics

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/depthbomb/tomogo/rest"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPRecordingPreservesRequestsAndExcludesPayloads(t *testing.T) {
	sink := &memorySink{}
	r := newRecorder(sink, nil)
	transport := Transport{
		Recorder: r,
		Service:  "steam",
		Base: transportFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Authorization") != "private-token" || req.URL.RawQuery != "q=private-query" {
				t.Error("diagnostics changed the request")
			}

			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader("private-response")),
			}, nil
		}),
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.test/private-path?q=private-query", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "private-token")
	response, err := transport.RoundTrip(req)
	if err != nil || response.StatusCode != 429 {
		t.Fatalf("transport response = %v, %v", response, err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "private-response" {
		t.Fatal("diagnostics consumed the response")
	}
	observer := RESTObserver{
		Recorder: r,
	}
	observer.RequestFinished(rest.Route{}, nil, &rest.Error{
		StatusCode: 403,
		Code:       50013,
		Message:    "private-discord-message",
	}, 50*time.Millisecond)
	observer.RateLimited(rest.Route{}, rest.RateLimit{
		Bucket:     "private-bucket",
		RetryAfter: 2 * time.Second,
	})
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	data := sink.Bytes()
	if bytes.Contains(data, []byte("private-")) || !bytes.Contains(data, []byte(`"http_status":429`)) || !bytes.Contains(data, []byte(`"discord_code":50013`)) || !bytes.Contains(data, []byte(`"category":"rate_limit"`)) {
		t.Fatal("HTTP diagnostics leaked input or omitted status/rate information")
	}
}
