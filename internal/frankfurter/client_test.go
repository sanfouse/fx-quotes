package frankfurter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fx-quotes/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRateWithRetry(t *testing.T) {
	const validRate = `{"date":"2026-11-05","base":"EUR","quote":"MXN","rate":20.95}`
	tests := []struct {
		name     string
		statuses []int
		body     string
		want     float64
	}{
		{name: "valid rate", statuses: []int{200}, body: validRate, want: 20.95},
		{name: "success on third attempt", statuses: []int{503, 502, 200}, body: validRate, want: 20.95},
		{name: "attempts exhausted", statuses: []int{503, 503, 503}},
		{name: "client error", statuses: []int{404}},
		{name: "malformed JSON", statuses: []int{200}, body: `{"rate":`},
		{name: "wrong pair", statuses: []int{200}, body: `{"base":"USD","quote":"MXN","rate":18.64}`},
		{name: "invalid price", statuses: []int{200}, body: `{"base":"EUR","quote":"MXN","rate":0}`},
		{name: "large response", statuses: []int{200}, body: strings.Repeat(" ", 4097)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/provider/v2/rate/EUR/MXN", r.URL.Path)
				attempt := int(calls.Add(1))
				status := tt.statuses[min(attempt, len(tt.statuses))-1]
				w.WriteHeader(status)
				if status != http.StatusOK {
					return
				}
				_, err := io.WriteString(w, tt.body)
				assert.NoError(t, err)
			}))
			defer server.Close()
			settings := config.Defaults().Rates
			settings.BaseURL = server.URL + "/provider/"
			rates := NewClient(server.Client(), settings)

			got, err := rates.GetRate(t.Context(), "EUR/MXN")
			if tt.want == 0 {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
			assert.Equal(t, int32(len(tt.statuses)), calls.Load(), "upstream requests")
		})
	}
}

func TestGetRateTimeout(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()
	httpClient := server.Client()
	httpClient.Timeout = 100 * time.Millisecond
	settings := config.Defaults().Rates
	settings.BaseURL = server.URL
	rates := NewClient(httpClient, settings)
	_, err := rates.GetRate(t.Context(), "EUR/MXN")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, int32(3), calls.Load(), "upstream requests")
}
