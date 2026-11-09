package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"fx-quotes/internal/frankfurter"
	"fx-quotes/internal/handler"
	"fx-quotes/internal/handler/dto"
	"fx-quotes/internal/postgres"
	"fx-quotes/internal/service"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQuoteFlow(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL must point to an empty, migrated test database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
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
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(cleanupCtx, `DELETE FROM quotes
			WHERE idempotency_key IN ('test-key', 'timeout-key', 'recovery-key')`)
		if err != nil {
			t.Errorf("delete test quotes: %v", err)
		}
	}()

	started := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
				if _, err := io.WriteString(w, `{"base":"EUR","quote":"MXN","rate":20.95}`); err != nil {
					t.Errorf("write test rate: %v", err)
				}
			case <-r.Context().Done():
			}
			return
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()
	defer unblock()
	client := upstream.Client()
	client.Timeout = 3 * time.Second

	repo := postgres.NewQuoteRepo(pool)
	app := handler.NewQuoteHandler(service.NewQuoteService(repo), config.Defaults().HTTP).Routes()
	ratesSettings := config.Defaults().Rates
	ratesSettings.BaseURL = upstream.URL
	worker := service.NewQuoteWorker(repo, frankfurter.NewClient(client, ratesSettings), config.Defaults().Worker)
	stopWorker := startWorker(ctx, worker)
	defer stopWorker()

	post := func(key string) dto.QuoteResponse {
		return requestQuote(t, app, http.MethodPost, "/quotes", `{"pair":"EUR/MXN"}`, key, http.StatusAccepted)
	}
	first := post("test-key")
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("worker did not call upstream")
	}
	if first.ID <= 0 || post("test-key").ID != first.ID {
		t.Fatalf("unexpected ID for idempotent job: %d", first.ID)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, lockErr := tx.Exec(ctx, "SELECT id FROM quotes WHERE id = $1 FOR UPDATE NOWAIT", first.ID)
	rollbackErr := tx.Rollback(ctx)
	if lockErr != nil || rollbackErr != nil {
		t.Fatalf("row locked during HTTP: lock=%v rollback=%v", lockErr, rollbackErr)
	}
	firstPath := fmt.Sprintf("/quotes/%d", first.ID)
	pending := requestQuote(t, app, http.MethodGet, firstPath, "", "", http.StatusAccepted)
	if pending.Status != domain.QuotePending || pending.Price != nil || pending.UpdatedAt != nil {
		t.Fatalf("unexpected pending response: %+v", pending)
	}
	const latestPath = "/quotes/latest?pair=EUR%2FMXN"
	requestQuote(t, app, http.MethodGet, latestPath, "", "", http.StatusNotFound)

	unblock()
	complete := waitForQuote(t, ctx, repo, first.ID, domain.QuoteCompleted)
	if complete.Price != 20.95 || complete.UpdatedAt.IsZero() || complete.UpdatedAt.Location() != time.UTC {
		t.Fatalf("unexpected completed quote: %+v", complete)
	}
	if post("test-key").ID != first.ID {
		t.Fatal("completed job lost its idempotency key")
	}

	second := post("timeout-key")
	if second.ID <= first.ID {
		t.Fatalf("expected new update, got ID %d", second.ID)
	}
	waitForQuote(t, ctx, repo, second.ID, domain.QuoteFailed)
	failed := requestQuote(t, app, http.MethodGet, fmt.Sprintf("/quotes/%d", second.ID), "", "", http.StatusOK)
	if failed.Status != domain.QuoteFailed || failed.Error != "rate_unavailable" || failed.Price != nil {
		t.Fatalf("unexpected failed response: %+v", failed)
	}
	latest := requestQuote(t, app, http.MethodGet, latestPath, "", "", http.StatusOK)
	if latest.ID != first.ID || latest.Status != domain.QuoteCompleted {
		t.Fatalf("unexpected latest response: %+v", latest)
	}

	stopWorker()

	third := post("recovery-key")
	fetchStarted := make(chan struct{})
	stopCancelledWorker := startWorker(ctx, service.NewQuoteWorker(repo, testRates(func(ctx context.Context, _ string) (float64, error) {
		close(fetchStarted)
		<-ctx.Done()
		return 0, ctx.Err()
	}), config.Defaults().Worker))
	defer stopCancelledWorker()
	select {
	case <-fetchStarted:
	case <-ctx.Done():
		t.Fatal("worker did not claim the job before cancellation")
	}
	stopCancelledWorker()
	waitForQuote(t, ctx, repo, third.ID, domain.QuotePending)
	if _, err := pool.Exec(ctx, "UPDATE quotes SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", third.ID); err != nil {
		t.Fatal(err)
	}
	stopRecoveryWorker := startWorker(ctx, service.NewQuoteWorker(repo, testRates(func(context.Context, string) (float64, error) {
		return 21, nil
	}), config.Defaults().Worker))
	defer stopRecoveryWorker()
	waitForQuote(t, ctx, repo, third.ID, domain.QuoteCompleted)
	latest = requestQuote(t, app, http.MethodGet, latestPath, "", "", http.StatusOK)
	if latest.ID != third.ID || latest.Price == nil || *latest.Price != 21 {
		t.Fatalf("latest response after new quote: %+v", latest)
	}
}

func requestQuote(t *testing.T, app http.Handler, method, path, body, key string, status int) dto.QuoteResponse {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer func() { _ = resp.Body.Close() }()
	var result dto.QuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		t.Fatalf("%s %s returned %d, want %d: %+v", method, path, resp.StatusCode, status, result)
	}
	return result
}

func waitForQuote(t *testing.T, ctx context.Context, repo *postgres.QuoteRepo, id int64, status domain.QuoteStatus) domain.Quote {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := repo.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status == status {
			return result
		}
		if result.Status != domain.QuotePending {
			t.Fatalf("quote %d reached %s, want %s", id, result.Status, status)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("quote %d did not reach %s: %v", id, status, ctx.Err())
		case <-ticker.C:
		}
	}
}

type testRates func(context.Context, string) (float64, error)

func (f testRates) GetRate(ctx context.Context, pair string) (float64, error) {
	return f(ctx, pair)
}

func startWorker(ctx context.Context, worker *service.QuoteWorker) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}
