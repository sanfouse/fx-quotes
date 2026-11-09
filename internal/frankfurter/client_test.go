package frankfurter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"fx-quotes/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRate(t *testing.T) {
	const validRate = `{"base":"EUR","quote":"MXN","rate":20.95}`
	tests := []struct {
		name     string
		statuses []int
		body     string
		want     float64
		wantErr  bool
	}{
		{name: "success", statuses: []int{200}, body: validRate, want: 20.95},
		{name: "retry succeeds", statuses: []int{503, 200}, body: validRate, want: 20.95},
		{name: "retries exhausted", statuses: []int{503, 503, 503}, wantErr: true},
		{name: "client error is not retried", statuses: []int{404}, wantErr: true},
		{name: "invalid JSON", statuses: []int{200}, body: `{"rate":`, wantErr: true},
		{name: "wrong pair", statuses: []int{200}, body: `{"base":"USD","quote":"MXN","rate":18.64}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					assert.Equal(t, http.MethodGet, req.Method)
					assert.Equal(t, "https://rates.test/provider/v2/rate/EUR/MXN", req.URL.String())
					status := tt.statuses[min(calls, len(tt.statuses)-1)]
					calls++
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
				})}
				settings := config.Defaults().Rates
				settings.BaseURL = "https://rates.test/provider/"

				got, err := NewClient(client, settings).GetRate(t.Context(), "EUR/MXN")
				if tt.wantErr {
					assert.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, tt.want, got)
				assert.Equal(t, len(tt.statuses), calls)
			})
		})
	}
}

func TestGetRateTransportError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("connection lost")
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, cause
		})}
		_, err := NewClient(client, config.Defaults().Rates).GetRate(t.Context(), "EUR/MXN")
		assert.ErrorIs(t, err, cause)
		assert.Equal(t, 3, calls)
	})
}

func TestGetRateCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("request made with canceled context")
		return nil, context.Canceled
	})}
	_, err := NewClient(client, config.Defaults().Rates).GetRate(ctx, "EUR/MXN")
	assert.ErrorIs(t, err, context.Canceled)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
