package frankfurter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fx-quotes/internal/config"
)

func TestGetRateWithRetry(t *testing.T) {
	const validRate = `{"date":"2026-11-05","base":"EUR","quote":"MXN","rate":20.95}`
	cases := []struct {
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/provider/v2/rate/EUR/MXN" {
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
				}
				attempt := int(calls.Add(1))
				status := tc.statuses[min(attempt, len(tc.statuses))-1]
				w.WriteHeader(status)
				if status != http.StatusOK {
					return
				}
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Errorf("write test response: %v", err)
				}
			}))
			defer server.Close()
			rates := NewClient(server.URL+"/provider/", server.Client(), config.Defaults().Rates)
			got, err := rates.GetRate(t.Context(), "EUR/MXN")
			if got != tc.want || (err != nil) != (tc.want == 0) {
				t.Errorf("got rate %v, error %v, want rate %v", got, err, tc.want)
			}
			if got := int(calls.Load()); got != len(tc.statuses) {
				t.Errorf("got %d upstream requests, want %d", got, len(tc.statuses))
			}
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
	rates := NewClient(server.URL, httpClient, config.Defaults().Rates)
	if _, err := rates.GetRate(t.Context(), "EUR/MXN"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("got %d upstream requests, want 3", got)
	}
}
