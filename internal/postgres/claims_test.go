package postgres_test

import (
	"testing"
	"time"

	"fx-quotes/internal/domain"
	"fx-quotes/internal/postgres"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuoteClaims(t *testing.T) {
	tests := []struct {
		name       string
		wantStatus domain.QuoteStatus
		wantPrice  float64
	}{
		{name: "complete", wantStatus: domain.QuoteCompleted, wantPrice: 1.1},
		{name: "fail", wantStatus: domain.QuoteFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, pool := openTestDB(t, 0, "claim-"+tt.name)
			repo := postgres.NewQuoteRepo(pool)
			id, err := repo.Enqueue(ctx, "EUR/USD", "claim-"+tt.name)
			require.NoError(t, err)
			type claim struct {
				job   domain.QuoteJob
				found bool
				err   error
			}
			const workers = 8
			results := make(chan claim, workers)
			start := make(chan struct{})
			for range workers {
				go func() {
					<-start
					job, found, err := repo.ClaimNext(ctx, 30*time.Second)
					results <- claim{job: job, found: found, err: err}
				}()
			}
			close(start)
			var first domain.QuoteJob
			claims := 0
			for range workers {
				result := <-results
				assert.NoError(t, result.err)
				if result.found {
					claims++
					first = result.job
				}
			}
			require.Equal(t, 1, claims)
			require.Equal(t, id, first.ID)

			assertLost := func(job domain.QuoteJob) {
				t.Helper()
				assert.ErrorIs(t, repo.Complete(ctx, job, 99), domain.ErrClaimLost, "stale complete: %+v", job)
				assert.ErrorIs(t, repo.Fail(ctx, job), domain.ErrClaimLost, "stale fail: %+v", job)
			}
			expireClaim(t, ctx, pool, id)
			assertLost(first)
			second, found, err := repo.ClaimNext(ctx, 30*time.Second)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, id, second.ID)
			require.Greater(t, second.Version, first.Version)
			assertLost(first)
			if tt.wantStatus == domain.QuoteCompleted {
				err = repo.Complete(ctx, second, tt.wantPrice)
			} else {
				err = repo.Fail(ctx, second)
			}
			require.NoError(t, err)
			assertLost(first)
			assertLost(second)
			quote := waitForQuote(t, ctx, repo, id, tt.wantStatus)
			assert.Equal(t, tt.wantPrice, quote.Price, "stale result cannot change price")
			_, found, err = repo.ClaimNext(ctx, time.Second)
			require.NoError(t, err)
			assert.False(t, found, "terminal quote cannot be claimed")
		})
	}
}
