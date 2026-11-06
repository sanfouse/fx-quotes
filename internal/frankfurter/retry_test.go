package frankfurter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"fx-quotes/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRetryPreservesCause(t *testing.T) {
	cause := errors.New("upstream connection lost")
	tests := []struct {
		name      string
		transport roundTripFunc
	}{
		{name: "transport error", transport: func(*http.Request) (*http.Response, error) {
			return nil, cause
		}},
		{name: "body read error", transport: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(iotest.ErrReader(cause)),
			}, nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls int
				client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					return tt.transport(req)
				})}
				rates := NewClient("https://example.com", client, config.Defaults().Rates)
				if _, err := rates.GetRate(t.Context(), "EUR/MXN"); !errors.Is(err, cause) {
					t.Fatalf("got %v, want original error %v", err, cause)
				}
				if calls != 3 {
					t.Fatalf("got %d requests, want 3", calls)
				}
			})
		})
	}
}

func TestRetryCanceledBeforeRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})}
	rates := NewClient("https://example.com", client, config.Defaults().Rates)
	if _, err := rates.GetRate(ctx, "EUR/MXN"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context canceled", err)
	}
	if calls != 0 {
		t.Fatalf("got %d requests for canceled context, want 0", calls)
	}
}

func TestRetryCanceledDuringDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var calls atomic.Int32
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("connection lost")
		})}
		rates := NewClient("https://example.com", client, config.Defaults().Rates)
		done := make(chan error, 1)
		go func() {
			_, err := rates.GetRate(ctx, "EUR/MXN")
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context canceled", err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("got %d requests after canceling retry delay, want 1", got)
		}
	})
}

func TestConfiguredRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := config.Defaults().Rates
		settings.MaxAttempts = 2
		settings.RetryDelay = 75 * time.Millisecond
		var calls int
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("connection lost")
		})}
		started := time.Now()
		if _, err := NewClient("https://example.com", client, settings).GetRate(t.Context(), "EUR/MXN"); err == nil {
			t.Fatal("expected provider error")
		}
		if calls != settings.MaxAttempts || time.Since(started) != settings.RetryDelay {
			t.Fatalf("calls=%d, elapsed=%s", calls, time.Since(started))
		}
	})
}
