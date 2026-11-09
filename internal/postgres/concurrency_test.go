package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fx-quotes/internal/config"
	"fx-quotes/internal/domain"
	"fx-quotes/internal/handler"
	"fx-quotes/internal/handler/dto"
	"fx-quotes/internal/postgres"
	"fx-quotes/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestsWithoutKeyCreateIndependentQuotes(t *testing.T) {
	_, pool := openTestDB(t, 0)
	repo := postgres.NewQuoteRepo(pool)
	app := handler.NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()
	var ids []int64
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, "DELETE FROM quotes WHERE id = ANY($1::bigint[])", ids)
		assert.NoError(t, err)
	})
	for range 2 {
		quote := requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, "", http.StatusAccepted)
		ids = append(ids, quote.ID)
	}
	assert.NotEqual(t, ids[0], ids[1])
}

func TestConcurrentRequestsWithSameKeyCreateOneQuote(t *testing.T) {
	_, pool := openTestDB(t, 0, "concurrency-key")
	repo := postgres.NewQuoteRepo(pool)
	app := handler.NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()
	type result struct {
		id  int64
		err error
	}
	const requests = 8
	results := make(chan result, requests)
	start := make(chan struct{})
	for range requests {
		go func() {
			<-start
			req := httptest.NewRequest(http.MethodPost, "/quotes", strings.NewReader(`{"pair":"EUR/MXN"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "concurrency-key")
			resp, err := app.Test(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer func() { _ = resp.Body.Close() }()
			var reference dto.QuoteReference
			err = json.NewDecoder(resp.Body).Decode(&reference)
			if resp.StatusCode != http.StatusAccepted {
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			results <- result{id: reference.ID, err: err}
		}()
	}
	close(start)
	var got []result
	for range requests {
		got = append(got, <-results)
	}
	for _, result := range got {
		require.NoError(t, result.err)
		assert.Positive(t, result.id)
		assert.Equal(t, got[0].id, result.id)
	}
	requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"USD/MXN"}`, "concurrency-key", http.StatusConflict)
}

func TestWorkersSkipClaimedQuote(t *testing.T) {
	ctx, pool := openTestDB(t, 0, "concurrency-first", "concurrency-second")
	repo := postgres.NewQuoteRepo(pool)
	first, err := repo.Enqueue(ctx, "EUR/USD", "concurrency-first")
	require.NoError(t, err)
	second, err := repo.Enqueue(ctx, "USD/MXN", "concurrency-second")
	require.NoError(t, err)
	attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	var firstCalls, secondCalls atomic.Int32
	stopFirstWorker := startWorker(t, attempt, service.NewQuoteWorker(repo, testRates(func(ctx context.Context, pair string) (float64, error) {
		if pair != "EUR/USD" {
			return 0, fmt.Errorf("first worker picked %s", pair)
		}
		if firstCalls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return 1.1, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}), config.Defaults().Worker))
	waitForSignal(t, attempt, started)
	stopSecondWorker := startWorker(t, attempt, service.NewQuoteWorker(repo, testRates(func(_ context.Context, pair string) (float64, error) {
		secondCalls.Add(1)
		if pair != "USD/MXN" {
			return 0, fmt.Errorf("second worker picked claimed pair %s", pair)
		}
		return 20, nil
	}), config.Defaults().Worker))
	waitForQuote(t, attempt, repo, second, domain.QuoteCompleted)
	stopSecondWorker()
	waitForQuote(t, attempt, repo, first, domain.QuotePending)
	_, found, err := repo.ClaimNext(attempt, config.Defaults().Worker.Lease)
	require.NoError(t, err)
	assert.False(t, found, "no jobs available while first worker holds its claim")

	unblock()
	waitForQuote(t, attempt, repo, first, domain.QuoteCompleted)
	stopFirstWorker()
	assert.Equal(t, int32(1), firstCalls.Load(), "first provider calls")
	assert.Equal(t, int32(1), secondCalls.Load(), "second provider calls")
}
