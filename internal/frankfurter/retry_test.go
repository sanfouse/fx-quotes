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

	"github.com/stretchr/testify/assert"
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
				rates := NewClient(client, config.Defaults().Rates)
				_, err := rates.GetRate(t.Context(), "EUR/MXN")
				assert.ErrorIs(t, err, cause)
				assert.Equal(t, 3, calls, "upstream requests")
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
	rates := NewClient(client, config.Defaults().Rates)
	_, err := rates.GetRate(ctx, "EUR/MXN")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls, "requests for canceled context")
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
		rates := NewClient(client, config.Defaults().Rates)
		done := make(chan error, 1)
		go func() {
			_, err := rates.GetRate(ctx, "EUR/MXN")
			done <- err
		}()
		synctest.Wait()
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
		assert.Equal(t, int32(1), calls.Load(), "requests after canceling retry delay")
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
		_, err := NewClient(client, settings).GetRate(t.Context(), "EUR/MXN")
		assert.Error(t, err)
		assert.Equal(t, settings.MaxAttempts, calls)
		assert.Equal(t, settings.RetryDelay, time.Since(started))
	})
}
