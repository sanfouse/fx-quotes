package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"fx-quotes/internal/domain"
	"fx-quotes/internal/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQuoteClaims(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL must point to an empty, migrated test database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
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
	repo := postgres.NewQuoteRepo(pool)
	for _, finish := range []string{"complete", "fail"} {
		t.Run(finish, func(t *testing.T) {
			id, err := repo.Enqueue(ctx, "EUR/USD", "claim-"+finish)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := pool.Exec(cleanupCtx, "DELETE FROM quotes WHERE id = $1", id); err != nil {
					t.Error(err)
				}
			}()
			type claim struct {
				job   domain.QuoteJob
				found bool
				err   error
			}
			results := make(chan claim, 8)
			start := make(chan struct{})
			for range 8 {
				go func() {
					<-start
					job, found, err := repo.ClaimNext(ctx, 30*time.Second)
					results <- claim{job, found, err}
				}()
			}
			close(start)
			var first domain.QuoteJob
			claims := 0
			for range 8 {
				result := <-results
				if result.err != nil {
					t.Error(result.err)
				}
				if result.found {
					claims++
					first = result.job
				}
			}
			if claims != 1 || first.ID != id {
				t.Fatalf("claims=%d job=%+v", claims, first)
			}

			if _, err := pool.Exec(ctx, "UPDATE quotes SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", id); err != nil {
				t.Fatal(err)
			}
			assertLost := func(job domain.QuoteJob) {
				t.Helper()
				if err := repo.Complete(ctx, job, 99); !errors.Is(err, domain.ErrClaimLost) {
					t.Fatalf("stale complete: %v", err)
				}
				if err := repo.Fail(ctx, job); !errors.Is(err, domain.ErrClaimLost) {
					t.Fatalf("stale fail: %v", err)
				}
			}
			assertLost(first)
			second, found, err := repo.ClaimNext(ctx, 30*time.Second)
			if err != nil || !found || second.ID != id || second.Version <= first.Version {
				t.Fatalf("reclaim: job=%+v found=%t err=%v", second, found, err)
			}
			assertLost(first)
			wantStatus := domain.QuoteCompleted
			if finish == "complete" {
				err = repo.Complete(ctx, second, 1.1)
			} else {
				err = repo.Fail(ctx, second)
				wantStatus = domain.QuoteFailed
			}
			if err != nil {
				t.Fatal(err)
			}
			assertLost(first)
			assertLost(second)
			quote := waitForQuote(t, ctx, repo, id, wantStatus)
			if finish == "complete" && quote.Price != 1.1 {
				t.Fatalf("stale result changed price: %v", quote.Price)
			}
			if _, found, err := repo.ClaimNext(ctx, time.Second); err != nil || found {
				t.Fatalf("claimed terminal job: found=%t err=%v", found, err)
			}
		})
	}
}
