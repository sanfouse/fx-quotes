package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentUpdates(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL must point to an empty, migrated test database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var hasQuotes bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM quotes)").Scan(&hasQuotes); err != nil {
		t.Fatal(err)
	}
	if hasQuotes {
		t.Fatal("use a dedicated test database with an empty quotes table")
	}
	var unkeyedIDs []int64
	var keyedID int64
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(cleanupCtx, `DELETE FROM quotes
			WHERE idempotency_key IN ('concurrency-key', 'concurrency-first', 'concurrency-second')
			OR id = ANY($1::bigint[])`, unkeyedIDs)
		if err != nil {
			t.Errorf("delete test quotes: %v", err)
		}
	}()
	repo := postgres.NewQuoteRepo(pool)
	app := handler.NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()

	t.Run("requests without a key create independent updates", func(t *testing.T) {
		for range 2 {
			quote := requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, "", http.StatusAccepted)
			unkeyedIDs = append(unkeyedIDs, quote.ID)
		}
		if unkeyedIDs[0] == unkeyedIDs[1] {
			t.Fatal("requests without a key returned the same ID")
		}
	})

	t.Run("concurrent requests with one key create one update", func(t *testing.T) {
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
				results <- result{reference.ID, err}
			}()
		}
		close(start)
		var id int64
		for range requests {
			result := <-results
			if result.err != nil || result.id <= 0 {
				t.Errorf("enqueue: id=%d, error=%v", result.id, result.err)
				continue
			}
			if id != 0 && result.id != id {
				t.Errorf("got ID %d, want %d", result.id, id)
			}
			id = result.id
		}
		keyedID = id
		requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"USD/MXN"}`, "concurrency-key", http.StatusConflict)
	})

	stopDrainWorker := startWorker(ctx, service.NewQuoteWorker(repo, testRates(func(context.Context, string) (float64, error) {
		return 20, nil
	}), config.Defaults().Worker))
	defer stopDrainWorker()
	for _, id := range unkeyedIDs {
		waitForQuote(t, ctx, repo, id, domain.QuoteCompleted)
	}
	waitForQuote(t, ctx, repo, keyedID, domain.QuoteCompleted)
	stopDrainWorker()

	t.Run("workers skip a claimed job without processing it twice", func(t *testing.T) {
		first, err := repo.Enqueue(ctx, "EUR/USD", "concurrency-first")
		if err != nil {
			t.Fatal(err)
		}
		second, err := repo.Enqueue(ctx, "USD/MXN", "concurrency-second")
		if err != nil {
			t.Fatal(err)
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		started := make(chan struct{})
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		var firstCalls, secondCalls atomic.Int32
		stopFirstWorker := startWorker(attempt, service.NewQuoteWorker(repo, testRates(func(ctx context.Context, pair string) (float64, error) {
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
		defer stopFirstWorker()
		select {
		case <-started:
		case <-attempt.Done():
			t.Fatal("first worker did not claim its job")
		}
		stopSecondWorker := startWorker(attempt, service.NewQuoteWorker(repo, testRates(func(_ context.Context, pair string) (float64, error) {
			secondCalls.Add(1)
			if pair != "USD/MXN" {
				return 0, fmt.Errorf("second worker picked claimed pair %s", pair)
			}
			return 20, nil
		}), config.Defaults().Worker))
		defer stopSecondWorker()
		waitForQuote(t, attempt, repo, second, domain.QuoteCompleted)
		stopSecondWorker()
		waitForQuote(t, attempt, repo, first, domain.QuotePending)
		if _, found, err := repo.ClaimNext(attempt, config.Defaults().Worker.Lease); err != nil || found {
			t.Fatalf("expected no available jobs, found=%t, error=%v", found, err)
		}
		unblock()
		waitForQuote(t, attempt, repo, first, domain.QuoteCompleted)
		stopFirstWorker()
		if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
			t.Fatalf("provider calls: first=%d, second=%d; want one each", firstCalls.Load(), secondCalls.Load())
		}
	})
}
