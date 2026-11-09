package postgres_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/frankfurter"
	"fx-quotes/internal/handler"
	"fx-quotes/internal/postgres"
	"fx-quotes/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuoteFlow(t *testing.T) {
	ctx, pool := openTestDB(t, 1, "test-key")
	started := make(chan struct{})
	release := make(chan struct{})
	notify := sync.OnceFunc(func() { close(started) })
	unblock := sync.OnceFunc(func() { close(release) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notify()
		select {
		case <-release:
			_, err := io.WriteString(w, `{"base":"EUR","quote":"MXN","rate":20.95}`)
			assert.NoError(t, err)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(unblock)
	client := upstream.Client()
	client.Timeout = 3 * time.Second
	ratesSettings := config.Defaults().Rates
	ratesSettings.BaseURL = upstream.URL
	repo := postgres.NewQuoteRepo(pool)
	app := handler.NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()
	startWorker(t, ctx, service.NewQuoteWorker(repo, frankfurter.NewClient(client, ratesSettings), config.Defaults().Worker))

	first := requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, "test-key", http.StatusAccepted)
	waitForSignal(t, ctx, started)
	require.Positive(t, first.ID)
	repeated := requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, "test-key", http.StatusAccepted)
	assert.Equal(t, first.ID, repeated.ID, "pending job keeps its idempotency key")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, lockErr := tx.Exec(ctx, "SELECT id FROM quotes WHERE id = $1 FOR UPDATE NOWAIT", first.ID)
	rollbackErr := tx.Rollback(ctx)
	require.NoError(t, lockErr, "row remains unlocked during provider request")
	require.NoError(t, rollbackErr)
	pending := requestQuote(t, app, http.MethodGet, fmt.Sprintf("/quotes/%d", first.ID), "", "", http.StatusAccepted)
	assert.Equal(t, domain.QuotePending, pending.Status)
	assert.Nil(t, pending.Price)
	assert.Nil(t, pending.UpdatedAt)
	requestQuote(t, app, http.MethodGet, "/quotes/latest?pair=EUR%2FMXN", "", "", http.StatusNotFound)

	unblock()
	complete := waitForQuote(t, ctx, repo, first.ID, domain.QuoteCompleted)
	assert.Equal(t, 20.95, complete.Price)
	assert.False(t, complete.UpdatedAt.IsZero())
	assert.Same(t, time.UTC, complete.UpdatedAt.Location())
	repeated = requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, "test-key", http.StatusAccepted)
	assert.Equal(t, first.ID, repeated.ID, "completed job keeps its idempotency key")
}

func TestQuoteTimeoutKeepsLatest(t *testing.T) {
	ctx, pool := openTestDB(t, 0, "completed-key", "timeout-key")
	repo := postgres.NewQuoteRepo(pool)
	firstID := enqueueCompletedQuote(t, ctx, repo, "completed-key", 20.95)
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	client := upstream.Client()
	client.Timeout = 100 * time.Millisecond
	ratesSettings := config.Defaults().Rates
	ratesSettings.BaseURL = upstream.URL
	app := handler.NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()
	startWorker(t, ctx, service.NewQuoteWorker(repo, frankfurter.NewClient(client, ratesSettings), config.Defaults().Worker))

	second := requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, "timeout-key", http.StatusAccepted)
	assert.Greater(t, second.ID, firstID)
	waitForQuote(t, ctx, repo, second.ID, domain.QuoteFailed)
	failed := requestQuote(t, app, http.MethodGet, fmt.Sprintf("/quotes/%d", second.ID), "", "", http.StatusOK)
	assert.Equal(t, domain.QuoteFailed, failed.Status)
	assert.Equal(t, "rate_unavailable", failed.Error)
	assert.Nil(t, failed.Price)
	latest := requestQuote(t, app, http.MethodGet, "/quotes/latest?pair=EUR%2FMXN", "", "", http.StatusOK)
	assert.Equal(t, firstID, latest.ID)
	assert.Equal(t, domain.QuoteCompleted, latest.Status)
}

func TestQuoteRecoveryUpdatesLatest(t *testing.T) {
	ctx, pool := openTestDB(t, 0, "previous-key", "recovery-key")
	repo := postgres.NewQuoteRepo(pool)
	enqueueCompletedQuote(t, ctx, repo, "previous-key", 20.95)
	app := handler.NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()
	quote := requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, "recovery-key", http.StatusAccepted)
	started := make(chan struct{})
	stopCanceledWorker := startWorker(t, ctx, service.NewQuoteWorker(repo, testRates(func(ctx context.Context, _ string) (float64, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	}), config.Defaults().Worker))
	waitForSignal(t, ctx, started)
	stopCanceledWorker()
	waitForQuote(t, ctx, repo, quote.ID, domain.QuotePending)

	expireClaim(t, ctx, pool, quote.ID)
	startWorker(t, ctx, service.NewQuoteWorker(repo, testRates(func(context.Context, string) (float64, error) {
		return 21, nil
	}), config.Defaults().Worker))
	waitForQuote(t, ctx, repo, quote.ID, domain.QuoteCompleted)
	latest := requestQuote(t, app, http.MethodGet, "/quotes/latest?pair=EUR%2FMXN", "", "", http.StatusOK)
	assert.Equal(t, quote.ID, latest.ID)
	require.NotNil(t, latest.Price)
	assert.Equal(t, float64(21), *latest.Price)
}
