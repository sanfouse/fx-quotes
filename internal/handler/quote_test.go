package handler

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuoteRoutes(t *testing.T) {
	const completed = `{"id":3,"pair":"EUR/MXN","status":"completed","price":20.95,"updated_at":"2026-11-05T12:00:00Z"}`
	tests := []struct {
		name, method, path, body string
		quoteStatus              domain.QuoteStatus
		repoErr                  error
		wantStatus               int
		wantBody, wantLocation   string
	}{
		{name: "enqueue", method: "POST", path: "/quotes", body: `{"pair":" eur/mxn "}`, wantStatus: 202, wantBody: `{"id":3}`, wantLocation: "/quotes/3"},
		{name: "invalid JSON", method: "POST", path: "/quotes", body: `{"pair":`, wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "unsupported pair", method: "POST", path: "/quotes", body: `{"pair":"EUR/GBP"}`, wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "key conflict", method: "POST", path: "/quotes", body: `{"pair":"EUR/MXN"}`, repoErr: domain.ErrIdempotencyConflict, wantStatus: 409, wantBody: `{"error":"idempotency_conflict"}`},
		{name: "pending", method: "GET", path: "/quotes/3", quoteStatus: domain.QuotePending, wantStatus: 202, wantBody: `{"id":3,"pair":"EUR/MXN","status":"pending"}`},
		{name: "completed", method: "GET", path: "/quotes/3", quoteStatus: domain.QuoteCompleted, wantStatus: 200, wantBody: completed},
		{name: "failed", method: "GET", path: "/quotes/3", quoteStatus: domain.QuoteFailed, wantStatus: 200, wantBody: `{"id":3,"pair":"EUR/MXN","status":"failed","error":"rate_unavailable"}`},
		{name: "not found", method: "GET", path: "/quotes/99", repoErr: domain.ErrQuoteNotFound, wantStatus: 404, wantBody: `{"error":"not_found"}`},
		{name: "invalid ID", method: "GET", path: "/quotes/0", wantStatus: 400, wantBody: `{"error":"invalid_request"}`},
		{name: "latest", method: "GET", path: "/quotes/latest?pair=EUR%2FMXN", quoteStatus: domain.QuoteCompleted, wantStatus: 200, wantBody: completed},
		{name: "storage error", method: "GET", path: "/quotes/3", repoErr: errors.New("database unavailable"), wantStatus: 500, wantBody: `{"error":"internal_error"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := quoteRepoStub{
				quote: domain.Quote{ID: 3, Pair: "EUR/MXN", Status: tt.quoteStatus, Price: 20.95, UpdatedAt: time.Date(2026, time.November, 5, 12, 0, 0, 0, time.UTC)},
				err:   tt.repoErr,
			}
			app := NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "demo")

			resp, err := app.Test(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Equal(t, tt.wantLocation, resp.Header.Get("Location"))
			assert.Regexp(t, "^application/json", resp.Header.Get("Content-Type"))
			assert.JSONEq(t, tt.wantBody, string(body))
		})
	}
}

type quoteRepoStub struct {
	quote domain.Quote
	err   error
}

func (r quoteRepoStub) Enqueue(context.Context, string, string) (int64, error) {
	return r.quote.ID, r.err
}

func (r quoteRepoStub) GetByID(context.Context, int64) (domain.Quote, error) {
	return r.quote, r.err
}

func (r quoteRepoStub) GetLatest(context.Context, string) (domain.Quote, error) {
	return r.quote, r.err
}
