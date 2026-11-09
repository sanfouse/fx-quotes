package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/handler/dto"
	"fx-quotes/internal/postgres"
	"fx-quotes/internal/service"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T, maxConns int32, keys ...string) (context.Context, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL must point to an empty, migrated test database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	settings, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)
	if maxConns > 0 {
		settings.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, settings)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	var hasQuotes bool
	require.NoError(t, pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM quotes)").Scan(&hasQuotes))
	require.False(t, hasQuotes, "use a dedicated test database with an empty quotes table")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(cleanupCtx, "DELETE FROM quotes WHERE idempotency_key = ANY($1::text[])", keys)
		assert.NoError(t, err, "delete test quotes")
	})
	return ctx, pool
}

func requestQuote(t *testing.T, app *fiber.App, method, path, body, key string, status int) dto.QuoteResponse {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var result dto.QuoteResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, status, resp.StatusCode, "%s %s: %+v", method, path, result)
	return result
}

func waitForQuote(t *testing.T, ctx context.Context, repo *postgres.QuoteRepo, id int64, status domain.QuoteStatus) domain.Quote {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := repo.GetByID(ctx, id)
		require.NoError(t, err)
		if result.Status == status {
			return result
		}
		require.Equal(t, domain.QuotePending, result.Status, "quote %d: want %s", id, status)
		select {
		case <-ctx.Done():
			t.Fatalf("quote %d did not reach %s: %v", id, status, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForSignal(t *testing.T, ctx context.Context, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatalf("worker did not call provider: %v", ctx.Err())
	}
}

func expireClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) {
	t.Helper()
	_, err := pool.Exec(ctx, "UPDATE quotes SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", id)
	require.NoError(t, err)
}

func enqueueCompletedQuote(t *testing.T, ctx context.Context, repo *postgres.QuoteRepo, key string, price float64) int64 {
	t.Helper()
	id, err := repo.Enqueue(ctx, "EUR/MXN", key)
	require.NoError(t, err)
	job, found, err := repo.ClaimNext(ctx, config.Defaults().Worker.Lease)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, job.ID)
	require.NoError(t, repo.Complete(ctx, job, price))
	return id
}

type testRates func(context.Context, string) (float64, error)

func (f testRates) GetRate(ctx context.Context, pair string) (float64, error) {
	return f(ctx, pair)
}

func startWorker(t *testing.T, ctx context.Context, worker *service.QuoteWorker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()
	stop := func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return stop
}
